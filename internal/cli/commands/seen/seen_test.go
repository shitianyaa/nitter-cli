package seen_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/shitianyaa/twitter-cli/internal/cli"
)

// tempHome redirects the home directory to a fresh temp dir and neutralizes
// the settings and proxy env overrides.
func tempHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{
		"TWITTER_DEFAULT_LIMIT", "TWITTER_LOG_LEVEL", "TWITTER_LOG_FORMAT",
		"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
	} {
		t.Setenv(key, "")
	}
	return home
}

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut strings.Builder
	code := cli.Run(args, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

// seenPath is the default seen.json location under the (temp) home — the
// seen commands have no --state-dir; they always operate on the default
// layout.
func seenPath(t *testing.T, home string) string {
	t.Helper()
	return filepath.Join(home, ".twitter-cli", "state", "seen.json")
}

// seedSeen writes a seen.json fixture with two sources (distinct counts and
// timestamps, keys chosen so "tag:%23AI" sorts before "user:NASA") and
// returns the file path.
func seedSeen(t *testing.T, home string) string {
	t.Helper()
	path := seenPath(t, home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	body := `{"version":1,"sources":{` +
		`"tag:%23AI":{"initialized":true,"seen_ids":["302","301"],"watermark_ids":["302"],"updated_at":"2026-09-12T08:00:00Z"},` +
		`"user:NASA":{"initialized":true,"seen_ids":["102","101"],"watermark_ids":["102","101"],"updated_at":"2026-09-12T09:30:00Z"}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed seen.json: %v", err)
	}
	return path
}

// TestSeenListHumanLinesSortedBySource: one tab-separated summary line per
// source, sorted by key.
func TestSeenListHumanLinesSortedBySource(t *testing.T) {
	home := tempHome(t)
	seedSeen(t, home)

	code, out, errOut := runCLI(t, "seen", "list")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want one per source:\n%s", len(lines), out)
	}
	wantTag := "tag:%23AI\tinitialized=true\tseen=2\twatermark=1\t2026-09-12T08:00:00Z"
	wantUser := "user:NASA\tinitialized=true\tseen=2\twatermark=2\t2026-09-12T09:30:00Z"
	if lines[0] != wantTag || lines[1] != wantUser {
		t.Errorf("lines = %q / %q, want sorted %q then %q", lines[0], lines[1], wantTag, wantUser)
	}
}

// TestSeenListJSONAlwaysArray: --json prints a JSON array (even for a single
// source — it is a listing, the shape is stable) with the projected fields.
func TestSeenListJSONAlwaysArray(t *testing.T) {
	home := tempHome(t)
	seedSeen(t, home)

	code, out, errOut := runCLI(t, "seen", "list", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil {
		t.Fatalf("output is not one JSON array: %v\n%s", err, out)
	}
	if len(arr) != 2 {
		t.Fatalf("%d entries, want 2:\n%s", len(arr), out)
	}
	// Sorted by source: the tag entry comes first.
	first := arr[0]
	if first["source"] != "tag:%23AI" || first["initialized"] != true ||
		first["seen_count"] != float64(2) || first["watermark_count"] != float64(1) ||
		first["updated_at"] != "2026-09-12T08:00:00Z" {
		t.Errorf("first entry = %v, want the projected tag source", first)
	}
}

// TestSeenListSourceFilter: --source narrows the listing to one source
// (parsed like a watch source).
func TestSeenListSourceFilter(t *testing.T) {
	home := tempHome(t)
	seedSeen(t, home)

	code, out, _ := runCLI(t, "seen", "list", "--source", "user:NASA")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "user:NASA\tinitialized=true\tseen=2\twatermark=2\t2026-09-12T09:30:00Z" {
		t.Errorf("output = %q, want only the user:NASA line", out)
	}

	code, out, _ = runCLI(t, "seen", "list", "--source", "user:NASA", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(out), &arr); err != nil || len(arr) != 1 {
		t.Fatalf("output = %q (%v), want a 1-element array", out, err)
	}
}

// TestSeenListUninitializedEntryShape: an entry recorded without
// initialization renders initialized=false and a dash updated_at is only for
// zero times — a stored entry always has a stamp, so exercise the zero-time
// dash via a hand-written fixture entry.
func TestSeenListZeroUpdatedAtRendersDash(t *testing.T) {
	home := tempHome(t)
	path := seenPath(t, home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := `{"version":1,"sources":{"list:42":{"initialized":false,"seen_ids":[],"watermark_ids":[],"updated_at":"0001-01-01T00:00:00Z"}}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("seed seen.json: %v", err)
	}

	code, out, _ := runCLI(t, "seen", "list")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	want := "list:42\tinitialized=false\tseen=0\twatermark=0\t-"
	if strings.TrimSpace(out) != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// TestSeenListEmptyStore: no state file — the (empty) hint on stderr,
// nothing on stdout, exit 0. In --json mode the empty listing is a literal
// [] on stdout (machine consumers get machine output).
func TestSeenListEmptyStore(t *testing.T) {
	tempHome(t) // no seen.json at all

	code, out, errOut := runCLI(t, "seen", "list")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if strings.TrimSpace(errOut) != "(empty)" {
		t.Errorf("stderr = %q, want the (empty) hint", errOut)
	}

	code, out, _ = runCLI(t, "seen", "list", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("--json output = %q, want []", out)
	}
}

// TestSeenListSourceFilterNoMatchIsEmpty: a filter matching nothing is the
// same empty-listing contract (exit 0, (empty) on stderr).
func TestSeenListSourceFilterNoMatchIsEmpty(t *testing.T) {
	home := tempHome(t)
	seedSeen(t, home)

	code, out, errOut := runCLI(t, "seen", "list", "--source", "user:NOPE")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if strings.TrimSpace(errOut) != "(empty)" {
		t.Errorf("stderr = %q, want the (empty) hint", errOut)
	}
}

// TestSeenListInvalidSourceIsUsageError: --source is parsed like a watch
// source; garbage exits 2.
func TestSeenListInvalidSourceIsUsageError(t *testing.T) {
	tempHome(t)
	code, _, _ := runCLI(t, "seen", "list", "--source", "garbage")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

// TestSeenListCorruptStoreExitsOne: a corrupt state file is a hard error —
// no silent reset, exit 1.
func TestSeenListCorruptStoreExitsOne(t *testing.T) {
	home := tempHome(t)
	path := seenPath(t, home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("not json at all"), 0o600); err != nil {
		t.Fatalf("seed corrupt seen.json: %v", err)
	}

	code, _, errOut := runCLI(t, "seen", "list")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %q)", code, errOut)
	}
}

// TestSeenClearSingleWithConfirm: --source deletes exactly that entry and
// persists atomically.
func TestSeenClearSingleWithConfirm(t *testing.T) {
	home := tempHome(t)
	path := seedSeen(t, home)

	code, _, errOut := runCLI(t, "seen", "clear", "--source", "user:NASA", "--confirm")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seen.json: %v", err)
	}
	var file struct {
		Version int                       `json:"version"`
		Sources map[string]map[string]any `json:"sources"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("decode seen.json: %v", err)
	}
	if file.Version != 1 {
		t.Errorf("version = %d, want 1 (kept)", file.Version)
	}
	if _, ok := file.Sources["user:NASA"]; ok {
		t.Error("user:NASA still present after clear")
	}
	if _, ok := file.Sources["tag:%23AI"]; !ok {
		t.Error("tag:%23AI must survive a single-source clear")
	}
}

// TestSeenClearAllWithConfirm: without --source every entry goes, the
// version field stays.
func TestSeenClearAllWithConfirm(t *testing.T) {
	home := tempHome(t)
	path := seedSeen(t, home)

	code, _, errOut := runCLI(t, "seen", "clear", "--confirm")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seen.json: %v", err)
	}
	if !strings.Contains(string(data), `"version":1`) {
		t.Errorf("cleared file = %s, want version kept", data)
	}
	if !strings.Contains(string(data), `"sources":{}`) {
		t.Errorf("cleared file = %s, want an empty sources map", data)
	}
}

// TestSeenClearWithoutConfirmIsUsageError: the confirmation gate — a usage
// error (exit 2) with guidance, and the file is untouched.
func TestSeenClearWithoutConfirmIsUsageError(t *testing.T) {
	home := tempHome(t)
	path := seedSeen(t, home)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seen.json: %v", err)
	}

	for _, args := range [][]string{
		{"seen", "clear"},
		{"seen", "clear", "--source", "user:NASA"},
	} {
		code, _, errOut := runCLI(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stderr %q)", args, code, errOut)
		}
		if !strings.Contains(errOut, "--confirm") {
			t.Errorf("%v: stderr = %q, want guidance naming --confirm", args, errOut)
		}
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seen.json: %v", err)
	}
	if !slices.Equal(before, after) {
		t.Errorf("a refused clear changed the file:\nbefore %s\nafter  %s", before, after)
	}
}

// TestSeenClearMissingSourceIsIdempotent: clearing an absent source exits 0
// with a "not found" hint and leaves the file byte-identical.
func TestSeenClearMissingSourceIsIdempotent(t *testing.T) {
	home := tempHome(t)
	path := seedSeen(t, home)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seen.json: %v", err)
	}

	code, out, errOut := runCLI(t, "seen", "clear", "--source", "user:NOPE", "--confirm")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing", out)
	}
	if !strings.Contains(errOut, "not found") || !strings.Contains(errOut, "user:NOPE") {
		t.Errorf("stderr = %q, want a not-found hint naming the source", errOut)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seen.json: %v", err)
	}
	if !slices.Equal(before, after) {
		t.Errorf("an idempotent clear rewrote the file:\nbefore %s\nafter  %s", before, after)
	}
}

// TestSeenClearOnEmptyStore: nothing to clear — exit 0, still no state file
// (a no-op clear must not create one).
func TestSeenClearOnEmptyStore(t *testing.T) {
	home := tempHome(t)
	code, _, errOut := runCLI(t, "seen", "clear", "--confirm")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if _, err := os.Stat(seenPath(t, home)); !os.IsNotExist(err) {
		t.Errorf("clear created a state file (stat err = %v), want none", err)
	}
}

// TestSeenClearCorruptStoreExitsOne.
func TestSeenClearCorruptStoreExitsOne(t *testing.T) {
	home := tempHome(t)
	path := seenPath(t, home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{bogus"), 0o600); err != nil {
		t.Fatalf("seed corrupt seen.json: %v", err)
	}

	code, _, _ := runCLI(t, "seen", "clear", "--confirm")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}
