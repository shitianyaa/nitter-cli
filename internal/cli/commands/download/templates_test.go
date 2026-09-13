package download

// Unit tests for the download naming templates (plan M10 Task 5): template
// validation, rendering, sanitization, the directory placement and the
// per-ref collision suffixing. The batch-level integration (real downloader
// over httptest) lives in download_internal_test.go, the full-chain
// flag/config precedence in download_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
)

const (
	tid  = "2070000000000000100"
	tusr = "nasa"
)

// tfile is a minimal planned file for template tests.
func tfile(id, seq, kind, ext string) client.PlannedFile {
	return client.PlannedFile{StatusID: id, Seq: seq, Kind: kind, Ext: ext}
}

// captureWarn collects the engine's stderr warning lines.
type captureWarn struct{ lines []string }

func (c *captureWarn) Write(p []byte) (int, error) {
	c.lines = append(c.lines, string(p))
	return len(p), nil
}

func (c *captureWarn) count() int  { return len(c.lines) }
func (c *captureWarn) all() string { return strings.Join(c.lines, "") }

func TestRenderTemplate(t *testing.T) {
	vals := map[string]string{"id": "123", "seq": "2", "user": "nasa", "kind": "video", "ext": ".mp4"}
	for _, tc := range []struct{ tmpl, want string }{
		{"{id}", "123"},
		{"{id}-{seq}", "123-2"},
		{"{user}/{kind}-{seq}{ext}", "nasa/video-2.mp4"},
		{"literal", "literal"},
		{"brace} stays", "brace} stays"},
	} {
		if got := renderTemplate(tc.tmpl, vals); got != tc.want {
			t.Errorf("renderTemplate(%q) = %q, want %q", tc.tmpl, got, tc.want)
		}
	}
}

func TestValidateTemplate(t *testing.T) {
	for _, tc := range []struct {
		name    string
		tmpl    string
		allowed map[string]bool
		wantErr string
	}{
		{"all filename placeholders", "{id}-{seq}-{user}-{kind}-{ext}", filenamePlaceholders, ""},
		{"directory placeholders", "{user}/{kind}/{id}", directoryPlaceholders, ""},
		{"unknown", "{date}-{id}", filenamePlaceholders, `unknown placeholder {date}`},
		{"seq forbidden in directory", "{id}-{seq}", directoryPlaceholders, `unknown placeholder {seq}`},
		{"ext forbidden in directory", "{user}/{ext}", directoryPlaceholders, `unknown placeholder {ext}`},
		{"empty name", "{}", filenamePlaceholders, `unknown placeholder {}`},
		{"unclosed", "{id", filenamePlaceholders, `never closed`},
	} {
		err := validateTemplate(tc.tmpl, tc.allowed)
		if tc.wantErr == "" {
			if err != nil {
				t.Errorf("%s: err = %v, want nil", tc.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("%s: err = %v, want it to contain %q", tc.name, err, tc.wantErr)
		}
	}
}

func TestSanitizeName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"plain.jpg", "plain.jpg"},
		{`a\b:c*d?e"f<g>h|i`, "a_b_c_d_e_f_g_h_i"},
		{"ctrl\x01\x1f", "ctrl__"},
		{"del\x7f", "del_"},
		{"emoji-😀", "emoji-😀"}, // legal Unicode passes through
		{"", ""},
	} {
		if got := sanitizeName(tc.in); got != tc.want {
			t.Errorf("sanitizeName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestFilenameForDefaultIsByteIdentical pins the binding: with no template
// configured the rendered names are byte-identical to the pre-template
// behavior, including the cover's <id>-cover shape and the extension-less
// auto path.
func TestFilenameForDefaultIsByteIdentical(t *testing.T) {
	e := newNameEngine("", "", nil)
	if name, ext := e.filenameFor(tfile(tid, "1", "video", ".mp4"), tusr); name != tid+"-1.mp4" || ext != ".mp4" {
		t.Errorf("video: name/ext = %q/%q, want %q/.mp4", name, ext, tid+"-1.mp4")
	}
	if name, ext := e.filenameFor(tfile(tid, "1", "cover", ".jpg"), tusr); name != tid+"-cover.jpg" || ext != ".jpg" {
		t.Errorf("cover: name/ext = %q/%q, want %q/.jpg", name, ext, tid+"-cover.jpg")
	}
	if name, ext := e.filenameFor(tfile(tid, "2", "image", ""), tusr); name != tid+"-2" || ext != "" {
		t.Errorf("auto: name/ext = %q/%q, want %q/\"\"", name, ext, tid+"-2")
	}
}

func TestFilenameForCustomTemplates(t *testing.T) {
	e := newNameEngine("{user}-{kind}-{seq}{ext}", "", nil)
	if name, _ := e.filenameFor(tfile(tid, "1", "video", ".mp4"), tusr); name != "nasa-video-1.mp4" {
		t.Errorf("name = %q, want nasa-video-1.mp4", name)
	}

	// {ext} positioned mid-template.
	e2 := newNameEngine("{id}.photo{ext}", "", nil)
	if name, ext := e2.filenameFor(tfile(tid, "1", "image", ".jpg"), tusr); name != tid+".photo.jpg" || ext != ".jpg" {
		t.Errorf("mid {ext}: name/ext = %q/%q, want %q/.jpg", name, ext, tid+".photo.jpg")
	}
	// {ext} empty at plan time: the Content-Type decides at download, after
	// the rendered name (R-M9-4 flows through).
	if name, ext := e2.filenameFor(tfile(tid, "1", "image", ""), tusr); name != tid+".photo" || ext != "" {
		t.Errorf("mid {ext} auto: name/ext = %q/%q, want %q/\"\"", name, ext, tid+".photo")
	}

	// A template without any placeholder gets the extension appended.
	e3 := newNameEngine("photo", "", nil)
	if name, _ := e3.filenameFor(tfile(tid, "1", "image", ".jpg"), tusr); name != "photo.jpg" {
		t.Errorf("literal: name = %q, want photo.jpg", name)
	}

	// {user} renders the ref's user segment as given: empty for a bare ID.
	e4 := newNameEngine("{user}-{id}", "", nil)
	if name, _ := e4.filenameFor(tfile(tid, "1", "image", ".jpg"), ""); name != "-"+tid+".jpg" {
		t.Errorf("empty user: name = %q, want %q", name, "-"+tid+".jpg")
	}
}

func TestFilenameForSanitizesRenderedName(t *testing.T) {
	e := newNameEngine("<{kind}>", "", nil)
	if name, _ := e.filenameFor(tfile(tid, "1", "image", ".jpg"), tusr); name != "_image_.jpg" {
		t.Errorf("name = %q, want _image_.jpg", name)
	}
	// The planned extension is sanitized too, whether positioned or appended.
	e2 := newNameEngine("{id}{ext}", "", nil)
	if name, _ := e2.filenameFor(tfile(tid, "1", "image", ".j:p*g"), tusr); name != tid+".j_p_g" {
		t.Errorf("name = %q, want %q", name, tid+".j_p_g")
	}
	e3 := newNameEngine("x", "", nil)
	if name, _ := e3.filenameFor(tfile(tid, "1", "image", ".j:p*g"), tusr); name != "x.j_p_g" {
		t.Errorf("name = %q, want x.j_p_g", name)
	}
}

// TestFilenameForCoverIgnoresTemplate pins the cover invariant: covers are a
// Kind exception BY DESIGN and keep <id>-cover.<ext> under any template.
func TestFilenameForCoverIgnoresTemplate(t *testing.T) {
	e := newNameEngine("custom-{seq}", "", nil)
	if name, ext := e.filenameFor(tfile(tid, "1", "cover", ".jpg"), tusr); name != tid+"-cover.jpg" || ext != ".jpg" {
		t.Errorf("cover: name/ext = %q/%q, want %q/.jpg", name, ext, tid+"-cover.jpg")
	}
	if name, ext := e.filenameFor(tfile(tid, "1", "cover", ""), tusr); name != tid+"-cover" || ext != "" {
		t.Errorf("cover auto: name/ext = %q/%q, want %q/\"\"", name, ext, tid+"-cover")
	}
}

func TestFilenameForEmptyRenderWarnsOnce(t *testing.T) {
	w := &captureWarn{}
	e := newNameEngine("{user}", "", w)
	name, ext := e.filenameFor(tfile(tid, "1", "image", ""), "")
	if name != tid+"-1" || ext != "" {
		t.Fatalf("name/ext = %q/%q, want the default %q/\"\"", name, ext, tid+"-1")
	}
	if w.count() != 1 || !strings.Contains(w.all(), "empty name") {
		t.Fatalf("warnings = %d %q, want one empty-name warning", w.count(), w.all())
	}
	// The warning is one-shot per run; the fallback applies silently after.
	if name, _ := e.filenameFor(tfile(tid, "2", "image", ""), ""); name != tid+"-2" {
		t.Errorf("second render = %q, want the default", name)
	}
	if w.count() != 1 {
		t.Errorf("warnings = %d, want still 1 (one-shot)", w.count())
	}
}

func TestEngineInvalidFilenameWarnsOnceAndFallsBack(t *testing.T) {
	w := &captureWarn{}
	e := newNameEngine("{date}-{id}", "", w)
	if name, _ := e.filenameFor(tfile(tid, "1", "image", ".jpg"), tusr); name != tid+"-1.jpg" {
		t.Fatalf("name = %q, want the default render %q", name, tid+"-1.jpg")
	}
	if w.count() != 1 || !strings.Contains(w.all(), "filename_template") || !strings.Contains(w.all(), "{date}") {
		t.Fatalf("warnings = %d %q, want one filename_template warning naming {date}", w.count(), w.all())
	}
	if name, _ := e.filenameFor(tfile(tid, "2", "image", ".jpg"), tusr); name != tid+"-2.jpg" {
		t.Errorf("second render = %q, want the default", name)
	}
	if w.count() != 1 {
		t.Errorf("warnings = %d, want still 1 (one-shot per run)", w.count())
	}
}

func TestEngineSeparatorFilenameRejected(t *testing.T) {
	w := &captureWarn{}
	e := newNameEngine("a/b-{id}", "", w)
	if name, _ := e.filenameFor(tfile(tid, "1", "image", ".mp4"), tusr); name != tid+"-1.mp4" {
		t.Fatalf("name = %q, want the default render", name)
	}
	if w.count() != 1 || !strings.Contains(w.all(), "path separator") {
		t.Fatalf("warnings = %d %q, want one separator warning", w.count(), w.all())
	}
}

func TestEngineEmptyTemplateIsDefaultSilently(t *testing.T) {
	w := &captureWarn{}
	e := newNameEngine("", "", w)
	if name, _ := e.filenameFor(tfile(tid, "1", "image", ".jpg"), tusr); name != tid+"-1.jpg" {
		t.Fatalf("name = %q, want the default render", name)
	}
	if w.count() != 0 {
		t.Errorf("warnings = %d, want none (empty template IS the default)", w.count())
	}
}

func TestDirectoryFor(t *testing.T) {
	t.Run("empty template is flat", func(t *testing.T) {
		w := &captureWarn{}
		e := newNameEngine("", "", w)
		out := t.TempDir()
		dir, err := e.directoryFor(tfile(tid, "1", "video", ".mp4"), tusr, out)
		if err != nil || dir != out {
			t.Fatalf("dir = %q (%v), want flat %q", dir, err, out)
		}
		if w.count() != 0 {
			t.Errorf("warnings = %d, want none", w.count())
		}
	})

	t.Run("subdirs created on demand", func(t *testing.T) {
		e := newNameEngine("", "{user}/{kind}", nil)
		out := t.TempDir()
		dir, err := e.directoryFor(tfile(tid, "1", "video", ".mp4"), tusr, out)
		if err != nil {
			t.Fatalf("directoryFor: %v", err)
		}
		want := filepath.Join(out, "nasa", "video")
		if dir != want {
			t.Fatalf("dir = %q, want %q", dir, want)
		}
		if fi, serr := os.Stat(want); serr != nil || !fi.IsDir() {
			t.Fatalf("subdirectory not created: %v", serr)
		}
	})

	t.Run("empty user segment drops its level", func(t *testing.T) {
		e := newNameEngine("", "{user}/{id}", nil)
		out := t.TempDir()
		dir, err := e.directoryFor(tfile(tid, "1", "video", ".mp4"), "", out)
		if err != nil || dir != filepath.Join(out, tid) {
			t.Fatalf("dir = %q (%v), want %q", dir, err, filepath.Join(out, tid))
		}
	})

	t.Run("forbidden placeholder warns and stays flat", func(t *testing.T) {
		w := &captureWarn{}
		e := newNameEngine("", "{user}-{seq}", w)
		out := t.TempDir()
		dir, err := e.directoryFor(tfile(tid, "1", "video", ".mp4"), tusr, out)
		if err != nil || dir != out {
			t.Fatalf("dir = %q (%v), want flat", dir, err)
		}
		if w.count() != 1 || !strings.Contains(w.all(), "directory_template") {
			t.Fatalf("warnings = %d %q, want one directory_template warning", w.count(), w.all())
		}
	})

	t.Run("literal traversal warns and stays flat", func(t *testing.T) {
		w := &captureWarn{}
		e := newNameEngine("", "../escape", w)
		out := t.TempDir()
		dir, err := e.directoryFor(tfile(tid, "1", "video", ".mp4"), tusr, out)
		if err != nil || dir != out {
			t.Fatalf("dir = %q (%v), want flat", dir, err)
		}
		if w.count() != 1 {
			t.Fatalf("warnings = %d %q, want one", w.count(), w.all())
		}
	})

	t.Run("rendered traversal from a crafted user latches flat", func(t *testing.T) {
		w := &captureWarn{}
		e := newNameEngine("", "{user}/{id}", w)
		out := t.TempDir()
		dir, err := e.directoryFor(tfile(tid, "1", "video", ".mp4"), "..", out)
		if err != nil || dir != out {
			t.Fatalf("dir = %q (%v), want flat", dir, err)
		}
		// The latch: later files of the run stay flat without a new warning.
		dir, err = e.directoryFor(tfile(tid, "1", "video", ".mp4"), tusr, out)
		if err != nil || dir != out {
			t.Fatalf("post-latch dir = %q (%v), want flat", dir, err)
		}
		if w.count() != 1 {
			t.Errorf("warnings = %d, want exactly 1", w.count())
		}
	})

	t.Run("literal backslash rejected", func(t *testing.T) {
		w := &captureWarn{}
		e := newNameEngine("", `a\b`, w)
		out := t.TempDir()
		dir, err := e.directoryFor(tfile(tid, "1", "video", ".mp4"), tusr, out)
		if err != nil || dir != out {
			t.Fatalf("dir = %q (%v), want flat", dir, err)
		}
		if w.count() != 1 {
			t.Fatalf("warnings = %d %q, want one", w.count(), w.all())
		}
	})

	t.Run("mkdir failure is the entry error", func(t *testing.T) {
		e := newNameEngine("", "sub", nil)
		blocker := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
			t.Fatalf("write blocker: %v", err)
		}
		if _, err := e.directoryFor(tfile(tid, "1", "video", ".mp4"), tusr, blocker); err == nil {
			t.Fatal("directoryFor = nil error, want the mkdir failure")
		}
	})
}

func TestRefNamerSuffixesCollisionsWithinRef(t *testing.T) {
	w := &captureWarn{}
	e := newNameEngine("{id}", "", w)
	out := t.TempDir()
	n := e.newRefNamer()

	for i, want := range []string{tid + ".jpg", tid + "-2.jpg", tid + "-3.jpg"} {
		tgt, err := n.target(tfile(tid, "1", "image", ".jpg"), tusr, out)
		if err != nil {
			t.Fatalf("target %d: %v", i+1, err)
		}
		if tgt.name != want || !tgt.extKnown || tgt.dir != out {
			t.Errorf("target %d = %+v, want name %q in %q", i+1, tgt, want, out)
		}
	}
	if w.count() != 2 {
		t.Errorf("warnings = %d %q, want one per collision (2)", w.count(), w.all())
	}

	// The extension-less auto path suffixes the base; the Content-Type
	// extension lands after the suffix.
	n2 := e.newRefNamer()
	tgt, _ := n2.target(tfile(tid, "1", "image", ""), tusr, out)
	if tgt.name != tid || tgt.extKnown {
		t.Fatalf("auto target = %+v, want the bare base", tgt)
	}
	tgt, _ = n2.target(tfile(tid, "1", "image", ""), tusr, out)
	if tgt.name != tid+"-2" || tgt.extKnown {
		t.Errorf("auto collision target = %+v, want base %q", tgt, tid+"-2")
	}
}

func TestRefNamerScopeIsPerRef(t *testing.T) {
	e := newNameEngine("{id}", "", nil)
	out := t.TempDir()
	n1, n2 := e.newRefNamer(), e.newRefNamer()
	t1, _ := n1.target(tfile(tid, "1", "image", ".jpg"), tusr, out)
	t2, _ := n2.target(tfile(tid, "1", "image", ".jpg"), tusr, out)
	if t1.name != tid+".jpg" || t2.name != tid+".jpg" {
		t.Errorf("targets = %q/%q, want both unsuffixed (collision scope is per ref)", t1.name, t2.name)
	}
}

func TestWithCollisionSuffix(t *testing.T) {
	for _, tc := range []struct{ name, ext, want string }{
		{"a.jpg", ".jpg", "a-2.jpg"},         // suffix before the trailing ext
		{"a", "", "a-2"},                     // auto path: suffix at the end
		{".jpg-2070", ".jpg", ".jpg-2070-2"}, // ext positioned mid-name
	} {
		if got := withCollisionSuffix(tc.name, tc.ext, 2); got != tc.want {
			t.Errorf("withCollisionSuffix(%q, %q, 2) = %q, want %q", tc.name, tc.ext, got, tc.want)
		}
	}
}
