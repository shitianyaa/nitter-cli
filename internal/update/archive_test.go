package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// realChecksums is the actual checksums.txt shipped with v0.7.0. Note the
// Windows lines carry a leading '*' — sha256sum's binary-mode marker.
const realChecksums = `f4fd2519ddb668c6ec38bbd6d7e10f51a31b8cb641e4231989b69b048008777c  nitter-0.7.0-darwin-amd64.tar.gz
e1073234ccdb43b4f8f977e85278a1e1fc88ecde50d2b418d78a4227fd670e78  nitter-0.7.0-darwin-arm64.tar.gz
0411cfc84d8218c34f1d59bb021d3b5a89b1ccf87ccbe2cd3586e1c6d5c0e032  nitter-0.7.0-linux-amd64.tar.gz
79be07dc1c6bfa930a2f6204dad72b62b675a3b23431df267a6af9658ef630be  nitter-0.7.0-linux-arm64.tar.gz
9aa0803752b8c99097075d855d7f69cc1157f64931b679b04d12c6e16c1c0926 *nitter-0.7.0-windows-amd64.zip
2076e8a16a9169b84684b5cc76d4ccfca666ac50fc10f0f843dff6e98b8d296b *nitter-0.7.0-windows-arm64.zip
`

func TestArchiveNameMatchesRealReleaseNames(t *testing.T) {
	cases := []struct {
		version, goos, goarch, want string
	}{
		{"0.7.0", "darwin", "amd64", "nitter-0.7.0-darwin-amd64.tar.gz"},
		{"0.7.0", "darwin", "arm64", "nitter-0.7.0-darwin-arm64.tar.gz"},
		{"0.7.0", "linux", "amd64", "nitter-0.7.0-linux-amd64.tar.gz"},
		{"0.7.0", "linux", "arm64", "nitter-0.7.0-linux-arm64.tar.gz"},
		{"0.7.0", "windows", "amd64", "nitter-0.7.0-windows-amd64.zip"},
		{"0.7.0", "windows", "arm64", "nitter-0.7.0-windows-arm64.zip"},
	}
	for _, tc := range cases {
		if got := ArchiveName(tc.version, tc.goos, tc.goarch); got != tc.want {
			t.Errorf("ArchiveName(%q,%q,%q) = %q, want %q", tc.version, tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestBinaryName(t *testing.T) {
	if got := BinaryName("windows"); got != "nitter.exe" {
		t.Errorf("BinaryName(windows) = %q, want nitter.exe", got)
	}
	for _, goos := range []string{"linux", "darwin"} {
		if got := BinaryName(goos); got != "nitter" {
			t.Errorf("BinaryName(%s) = %q, want nitter", goos, got)
		}
	}
}

func TestParseChecksumsAcceptsRealFile(t *testing.T) {
	sums, err := ParseChecksums([]byte(realChecksums))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if len(sums) != 6 {
		t.Fatalf("parsed %d entries, want 6: %+v", len(sums), sums)
	}
	// The '*' binary marker must be stripped: the key is the bare filename.
	if _, ok := sums["nitter-0.7.0-windows-amd64.zip"]; !ok {
		t.Errorf("windows entry missing (the '*' marker was not handled): %+v", sums)
	}
	if got := sums["nitter-0.7.0-linux-amd64.tar.gz"]; got != "0411cfc84d8218c34f1d59bb021d3b5a89b1ccf87ccbe2cd3586e1c6d5c0e032" {
		t.Errorf("linux-amd64 sum = %q", got)
	}
}

func TestParseChecksumsRejectsMalformed(t *testing.T) {
	cases := []struct {
		name, data, want string
	}{
		{"short digest", "abc  file.tar.gz\n", "checksum"},
		{"non-hex digest", strings.Repeat("z", 64) + "  file.tar.gz\n", "hex"},
		{"uppercase digest", strings.ToUpper(strings.Repeat("a", 64)) + "  file.tar.gz\n", "hex"},
		{"missing filename", strings.Repeat("a", 64) + "  \n", "name"},
		{"no separator", "justonefield\n", "expected"},
	}
	for _, tc := range cases {
		got, err := ParseChecksums([]byte(tc.data))
		if err == nil {
			t.Errorf("%s: want error mentioning %q, got nil (returned %+v)", tc.name, tc.want, got)
			continue
		}
		if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
			t.Errorf("%s: error = %q, want it to mention %q", tc.name, err, tc.want)
		}
	}
}

// Blank lines are skipped, not rejected: the real checksums.txt ends with a
// newline, and a strict parser would reject the release's own file.
func TestParseChecksumsSkipsBlankLines(t *testing.T) {
	sums, err := ParseChecksums([]byte("\n\n" + realChecksums + "\n\n"))
	if err != nil {
		t.Fatalf("ParseChecksums: %v", err)
	}
	if len(sums) != 6 {
		t.Errorf("parsed %d entries, want 6", len(sums))
	}
	// An entirely blank document parses to an empty map, not an error.
	empty, err := ParseChecksums([]byte("\n  \n"))
	if err != nil {
		t.Fatalf("blank-only document: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("blank-only document parsed %d entries, want 0", len(empty))
	}
}

// tarGz builds a single-entry .tar.gz for the extraction tests.
func tarGz(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

// zipBytes builds a single-entry .zip for the extraction tests.
func zipBytes(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func TestExtractBinary(t *testing.T) {
	payload := []byte("#!/bin/sh\necho fake binary\n")

	t.Run("tar.gz", func(t *testing.T) {
		got, err := ExtractBinary(tarGz(t, "nitter", payload), "nitter-0.7.0-linux-amd64.tar.gz", "nitter")
		if err != nil {
			t.Fatalf("ExtractBinary: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("content mismatch")
		}
	})

	t.Run("zip", func(t *testing.T) {
		got, err := ExtractBinary(zipBytes(t, "nitter.exe", payload), "nitter-0.7.0-windows-amd64.zip", "nitter.exe")
		if err != nil {
			t.Fatalf("ExtractBinary: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Errorf("content mismatch")
		}
	})

	t.Run("missing binary entry", func(t *testing.T) {
		if _, err := ExtractBinary(tarGz(t, "other", payload), "nitter-0.7.0-linux-amd64.tar.gz", "nitter"); err == nil {
			t.Error("want error for an archive without the binary")
		}
	})

	t.Run("duplicate binary entry", func(t *testing.T) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for i := 0; i < 2; i++ {
			_ = tw.WriteHeader(&tar.Header{Name: "nitter", Mode: 0o755, Size: int64(len(payload))})
			_, _ = tw.Write(payload)
		}
		_ = tw.Close()
		_ = gz.Close()
		if _, err := ExtractBinary(buf.Bytes(), "nitter-0.7.0-linux-amd64.tar.gz", "nitter"); err == nil {
			t.Error("want error for duplicate binary entries")
		}
	})

	t.Run("non-regular entry", func(t *testing.T) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		_ = tw.WriteHeader(&tar.Header{Name: "nitter", Mode: 0o755, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
		_ = tw.Close()
		_ = gz.Close()
		if _, err := ExtractBinary(buf.Bytes(), "nitter-0.7.0-linux-amd64.tar.gz", "nitter"); err == nil {
			t.Error("want error for a symlink entry named like the binary")
		}
	})

	t.Run("unsupported extension", func(t *testing.T) {
		if _, err := ExtractBinary([]byte("x"), "nitter-0.7.0-linux-amd64.rar", "nitter"); err == nil {
			t.Error("want error for an unsupported archive type")
		}
	})

	t.Run("corrupt gzip", func(t *testing.T) {
		if _, err := ExtractBinary([]byte("not gzip"), "nitter-0.7.0-linux-amd64.tar.gz", "nitter"); err == nil {
			t.Error("want error for a corrupt archive")
		}
	})
}

func TestSHA256Hex(t *testing.T) {
	// Known vector: sha256("abc").
	got := SHA256Hex([]byte("abc"))
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Errorf("SHA256Hex(abc) = %q, want %q", got, want)
	}
}
