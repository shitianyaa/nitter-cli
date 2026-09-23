// Release archive handling: asset naming, checksum parsing and binary
// extraction. Pure functions over bytes — no filesystem and no network, so
// the verification logic is testable without touching an installation.
package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// BinaryName is the executable's name inside a release archive: the Windows
// build carries the .exe suffix, every other platform does not.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "nitter.exe"
	}
	return "nitter"
}

// ArchiveName is the release asset name for one platform, matching the
// packaging step of .github/workflows/release.yml verbatim:
// nitter-<version>-<goos>-<goarch>.tar.gz (or .zip on Windows).
func ArchiveName(version, goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("nitter-%s-%s-%s.zip", version, goos, goarch)
	}
	return fmt.Sprintf("nitter-%s-%s-%s.tar.gz", version, goos, goarch)
}

// digestShape is a lowercase 64-character hex SHA-256.
var digestShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ParseChecksums parses a checksums.txt into filename → lowercase digest.
// Both sha256sum text forms are accepted ("<hex>  <name>" and the binary-mode
// "<hex> *<name>"), which is what the release actually ships: its Windows
// entries carry the '*' marker.
func ParseChecksums(data []byte) (map[string]string, error) {
	sums := make(map[string]string)
	for i, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimRight(raw, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("checksums line %d: expected '<digest>  <name>'", i+1)
		}
		// sha256sum's binary mode prefixes the name with '*'.
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" {
			return nil, fmt.Errorf("checksums line %d: missing asset name", i+1)
		}
		digest := fields[0]
		if !digestShape.MatchString(digest) {
			return nil, fmt.Errorf("checksums line %d: digest is not a lowercase 64-character hex SHA-256", i+1)
		}
		sums[name] = digest
	}
	return sums, nil
}

// SHA256Hex is the lowercase hex SHA-256 of data.
func SHA256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// ExtractBinary returns the release binary's bytes from a .tar.gz or .zip
// archive. Exactly one regular file named want must be present: a missing
// entry, a duplicate, or a non-regular entry (e.g. a symlink) is an error, so
// a malformed archive can never silently yield the wrong bytes.
func ExtractBinary(archive []byte, archiveName, want string) ([]byte, error) {
	switch {
	case strings.HasSuffix(archiveName, ".tar.gz"):
		return extractTarGz(archive, want)
	case strings.HasSuffix(archiveName, ".zip"):
		return extractZip(archive, want)
	default:
		return nil, fmt.Errorf("unsupported release archive %q", archiveName)
	}
}

func extractTarGz(archive []byte, want string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("open tar.gz release archive: %w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	var content []byte
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar.gz release archive: %w", err)
		}
		if header.Name != want {
			continue
		}
		if content != nil || !header.FileInfo().Mode().IsRegular() {
			return nil, fmt.Errorf("release archive has an invalid binary entry %q", want)
		}
		content, err = io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read binary entry %q: %w", want, err)
		}
	}
	if content == nil {
		return nil, fmt.Errorf("release archive has no binary entry %q", want)
	}
	return content, nil
}

func extractZip(archive []byte, want string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("open zip release archive: %w", err)
	}
	var found *zip.File
	for _, file := range reader.File {
		if file.Name != want {
			continue
		}
		if found != nil || !file.FileInfo().Mode().IsRegular() {
			return nil, fmt.Errorf("release archive has an invalid binary entry %q", want)
		}
		found = file
	}
	if found == nil {
		return nil, fmt.Errorf("release archive has no binary entry %q", want)
	}
	entry, err := found.Open()
	if err != nil {
		return nil, fmt.Errorf("open zip binary entry %q: %w", want, err)
	}
	defer entry.Close()
	content, err := io.ReadAll(entry)
	if err != nil {
		return nil, fmt.Errorf("read zip binary entry %q: %w", want, err)
	}
	return content, nil
}
