package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// RepoSlug is the repository whose releases are installed. Mirrors
// buildinfo.Repo; kept here so internal/update stays free of CLI imports.
const RepoSlug = "shitianyaa/nitter-cli"

// assetURLPrefix is the trusted download prefix. A package variable so tests
// can point it at an httptest server; production never changes it.
var assetURLPrefix = "https://github.com/" + RepoSlug + "/releases/download/"

// downloadTimeout bounds a single asset download. Deliberately explicit: an
// implicit default here would be a silent timeout.
const downloadTimeout = 5 * time.Minute

// DownloadTimeout exposes the asset-download timeout so the command layer
// builds the same client the installer defaults to.
func DownloadTimeout() time.Duration { return downloadTimeout }

// verifyTimeout bounds the staged binary's --version probe.
const verifyTimeout = 30 * time.Second

// InstallOptions injects the system boundaries so the whole install flow is
// testable without ever replacing the test binary.
type InstallOptions struct {
	// GOOS / GOARCH select the platform asset; empty means runtime's.
	GOOS, GOARCH string
	// HTTPClient downloads the assets (nil → a client with downloadTimeout).
	HTTPClient *http.Client
	// ExecutablePath locates the binary to replace (nil → os.Executable).
	ExecutablePath func() (string, error)
	// Replace swaps the staged file into place (nil → the platform default).
	Replace func(src, dst string) error
	// VerifyVersion checks the staged binary before it is installed. Nil
	// builds VerifyStagedVersion from ExpectedVersion.
	VerifyVersion func(expectedVersion string) func(path string) error
	// ExpectedVersion is the release version the staged binary must report.
	ExpectedVersion string
}

// Installer downloads, verifies and installs one release for one platform.
type Installer struct {
	goos           string
	goarch         string
	httpClient     *http.Client
	executablePath func() (string, error)
	replace        func(src, dst string) error
	verifyVersion  func(path string) error
	archivePrefix  string
}

// NewInstaller assembles the production installer.
func NewInstaller(opts InstallOptions) *Installer {
	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := opts.GOARCH
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: downloadTimeout}
	}
	executablePath := opts.ExecutablePath
	if executablePath == nil {
		executablePath = os.Executable
	}
	replace := opts.Replace
	if replace == nil {
		replace = replaceExecutable
	}
	verify := opts.VerifyVersion
	if verify == nil {
		verify = VerifyStagedVersion
	}
	return &Installer{
		goos:           goos,
		goarch:         goarch,
		httpClient:     client,
		executablePath: executablePath,
		replace:        replace,
		verifyVersion:  verify(opts.ExpectedVersion),
		archivePrefix:  assetURLPrefix,
	}
}

// Install fetches the release's platform archive, verifies it end to end and
// atomically replaces the running binary. Every failure path leaves the
// existing installation untouched: nothing touches the target until the
// checksum, the archive structure and the staged binary's version all pass.
func (i *Installer) Install(ctx context.Context, rel Release) error {
	archiveName := ArchiveName(rel.Version, i.goos, i.goarch)
	archiveURL, ok := assetURLFor(rel, archiveName)
	if !ok {
		return fmt.Errorf("release %s has no asset %q for %s/%s", rel.Tag, archiveName, i.goos, i.goarch)
	}
	if err := i.validateAssetURL(archiveURL, rel.Tag, archiveName); err != nil {
		return err
	}

	checksumsURL, ok := assetURLFor(rel, "checksums.txt")
	if !ok {
		return fmt.Errorf("release %s has no checksums.txt asset", rel.Tag)
	}
	if err := i.validateAssetURL(checksumsURL, rel.Tag, "checksums.txt"); err != nil {
		return err
	}
	checksums, err := i.fetchURL(ctx, checksumsURL)
	if err != nil {
		return fmt.Errorf("download checksums.txt: %w", err)
	}
	sums, err := ParseChecksums(checksums)
	if err != nil {
		return err
	}
	wantSum, ok := sums[archiveName]
	if !ok {
		return fmt.Errorf("checksums.txt has no entry for %q", archiveName)
	}

	archive, err := i.fetchURL(ctx, archiveURL)
	if err != nil {
		return fmt.Errorf("download release archive %q: %w", archiveName, err)
	}
	if got := SHA256Hex(archive); got != wantSum {
		return fmt.Errorf("release archive %q failed SHA-256 verification (the existing installation is unchanged)", archiveName)
	}
	binary, err := ExtractBinary(archive, archiveName, BinaryName(i.goos))
	if err != nil {
		return err
	}

	executable, err := i.executablePath()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return fmt.Errorf("resolve current executable %q: %w", executable, err)
	}
	if link, err := filepath.EvalSymlinks(executable); err == nil {
		executable = link
	}

	staged, err := stage(executable, binary)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(staged) }()

	if err := i.verifyVersion(staged); err != nil {
		return fmt.Errorf("staged binary failed verification (the existing installation is unchanged): %w", err)
	}
	if err := i.replace(staged, executable); err != nil {
		return fmt.Errorf("replace %q: %w", executable, err)
	}
	return nil
}

// ValidateAssetURL exposes the official-URL check for callers that need the
// same trust boundary (the CLI reports a clear error before downloading).
func ValidateAssetURL(rawURL, tag, assetName string) error {
	want := assetURLPrefix + tag + "/" + assetName
	if rawURL != want {
		return fmt.Errorf("release asset %q has an untrusted download URL", assetName)
	}
	return nil
}

// validateAssetURL pins the download to the official release path, so a
// tampered API response cannot redirect the download elsewhere.
func (i *Installer) validateAssetURL(rawURL, tag, assetName string) error {
	want := i.archivePrefix + tag + "/" + assetName
	if rawURL != want {
		return fmt.Errorf("release asset %q has an untrusted download URL", assetName)
	}
	return nil
}

// stage writes content to a temp file BESIDE target (same filesystem, so the
// later rename is atomic) with the executable bit set.
func stage(target string, content []byte) (string, error) {
	f, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".stage-*")
	if err != nil {
		return "", fmt.Errorf("stage beside %q: %w", target, err)
	}
	path := f.Name()
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("write staged binary %q: %w", path, err)
	}
	if err := f.Chmod(0o755); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("chmod staged binary %q: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close staged binary %q: %w", path, err)
	}
	return path, nil
}

func assetURLFor(rel Release, name string) (string, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a.URL, true
		}
	}
	return "", false
}

func (i *Installer) fetchURL(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build download request: %w", err)
	}
	req.Header.Set("User-Agent", "nitter-cli-update")
	resp, err := i.httpClient.Do(req)
	if err != nil {
		return nil, redactURLError(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return nil, fmt.Errorf("read download: %w", err)
	}
	return data, nil
}

// VerifyStagedVersion returns a checker that executes the staged binary with
// --version and compares the reported version to expectedVersion. Running the
// candidate is safe at this point: its SHA-256 already matched the official
// checksums. This catches a packaging error where the archive's name and the
// binary inside it disagree — which a checksum alone cannot, since both come
// from the same build.
func VerifyStagedVersion(expectedVersion string) func(path string) error {
	want := strings.TrimPrefix(strings.TrimSpace(expectedVersion), "v")
	return func(path string) error {
		if want == "" {
			return nil
		}
		ctx, cancel := context.WithTimeout(context.Background(), verifyTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, path, "--version").Output()
		if err != nil {
			return fmt.Errorf("run staged binary: %w", err)
		}
		got := ParseVersionOutput(string(out))
		if got == "" {
			return fmt.Errorf("staged binary printed no recognisable version: %q", strings.TrimSpace(string(out)))
		}
		if got != want {
			return fmt.Errorf("staged binary reports version %q, but the release is %q", got, want)
		}
		return nil
	}
}

// ParseVersionOutput extracts the version from a `nitter --version` line
// ("nitter version 0.7.0").
func ParseVersionOutput(out string) string {
	fields := strings.Fields(strings.TrimSpace(out))
	for i, f := range fields {
		if f == "version" && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	return ""
}
