package circle_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shitianyaa/nitter-cli/internal/cli/client"
	"github.com/shitianyaa/nitter-cli/internal/cli/commands/circle"
	"github.com/shitianyaa/nitter-cli/internal/cli/invocation"
)

func fxProfileJSON(screenName string, followers int, bio string) map[string]any {
	return map[string]any{"code": 200, "user": map[string]any{
		"screen_name": screenName, "name": screenName, "followers": followers, "description": bio,
	}}
}

func fxAuthor(screenName string) map[string]any {
	return map[string]any{"screen_name": screenName, "name": screenName}
}

func fxRetweet(id, author string) map[string]any {
	return map[string]any{
		"id": id, "text": "rt", "is_retweet": true,
		"reposted_by": map[string]any{"name": "Seed", "screen_name": "seed"},
		"author":      fxAuthor(author),
		"created_at":  "Sun Jul 05 09:09:40 +0000 2026",
	}
}

// TestCircleSuggest_RankingAndSources: a handle followed AND retweeted ranks
// first with source "both"; retweet-only candidates are enriched via Profile()
// and carry the enriched followers/bio (so they can outrank a follow-only row).
func TestCircleSuggest_RankingAndSources(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.ToLower(r.URL.Path)
		switch {
		case strings.HasSuffix(path, "/following"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "results": []map[string]any{
				{"screen_name": "both1", "name": "both1", "followers": 5000, "description": "followed"},
				{"screen_name": "onlyfollow", "name": "onlyfollow", "followers": 999, "description": "f-only"},
			}})
		case strings.HasSuffix(path, "/statuses"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "results": []map[string]any{
				fxRetweet("11", "both1"),
				fxRetweet("12", "onlyrt"),
			}})
		case strings.HasSuffix(path, "/profile/onlyrt"):
			_ = json.NewEncoder(w).Encode(fxProfileJSON("onlyrt", 7000, "enriched bio"))
		default: // seed probe /2/profile/seed
			_ = json.NewEncoder(w).Encode(fxProfileJSON("seed", 123, "seed bio"))
		}
	}))
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, out, errOut := runCLI(t, "circle", "suggest", "seed", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	var got []map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, out)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (%s)", len(got), out)
	}
	if got[0]["handle"] != "both1" || got[0]["source"] != "both" || got[0]["followers_count"].(float64) != 5000 {
		t.Errorf("rank 0 = %+v, want both1/both/5000", got[0])
	}
	if got[1]["handle"] != "onlyrt" || got[1]["source"] != "retweet" || got[1]["followers_count"].(float64) != 7000 {
		t.Errorf("rank 1 = %+v, want enriched onlyrt/7000", got[1])
	}
	if got[1]["bio"] != "enriched bio" {
		t.Errorf("rank 1 bio = %v, want enriched bio", got[1]["bio"])
	}
	if got[2]["handle"] != "onlyfollow" {
		t.Errorf("rank 2 = %+v, want onlyfollow", got[2])
	}
}

// TestCircleSuggest_HumanOutputAndSummary: the human rendering prints the stats
// header, the ranked table, and a --min-followers filtered summary section;
// no summary row prints the honest "(no matches)" note on stderr.
func TestCircleSuggest_HumanOutputAndSummary(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.ToLower(r.URL.Path)
		switch {
		case strings.HasSuffix(path, "/following"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "results": []map[string]any{
				{"screen_name": "onlyfollow", "name": "onlyfollow", "followers": 1000, "description": "bio of onlyfollow"},
			}})
		case strings.HasSuffix(path, "/statuses"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "results": []map[string]any{}})
		default:
			_ = json.NewEncoder(w).Encode(fxProfileJSON("seed", 1, "s"))
		}
	}))
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	var outBuf, errBuf strings.Builder
	s := &invocation.Streams{Out: &outBuf, Err: &errBuf, OutIsTTY: true, CTX: context.Background()}
	cmd := circle.New(s)
	cmd.SetArgs([]string{"suggest", "seed", "--min-followers", "5000"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := outBuf.String()
	if !strings.Contains(out, "candidates for @seed") {
		t.Errorf("missing stats header: %q", out)
	}
	if !strings.Contains(out, "@onlyfollow\t1000\tbio of onlyfollow\tfollowing") {
		t.Errorf("missing main row: %q", out)
	}
	if !strings.Contains(out, "top matches (>= 5000 followers):") {
		t.Errorf("missing summary header: %q", out)
	}
	if !strings.Contains(errBuf.String(), "(no matches)") {
		t.Errorf("stderr = %q, want (no matches)", errBuf.String())
	}
}

// TestCircleSuggest_ExitCodes: usage errors exit 2 before any network; a
// missing seed exits 1; an empty candidate set exits 0 with [] on --json.
func TestCircleSuggest_ExitCodes(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)

	// No server wired below for these: any network attempt would surface as a
	// different failure, so exit 2 proves validation ran first.
	for _, args := range [][]string{
		{"circle", "suggest"},
		{"circle", "suggest", "bad-handle!"},
		{"circle", "suggest", "seed", "--limit", "0"},
		{"circle", "suggest", "seed", "--limit", "-1"},
		{"circle", "suggest", "seed", "--min-followers", "-1"},
		{"circle", "suggest", "seed", "extra"},
	} {
		code, _, errOut := runCLI(t, args...)
		if code != 2 {
			t.Errorf("%v: exit = %d, want 2 (stderr %q)", args, code, errOut)
		}
	}

	// Seed probe 404 -> exit 1.
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":404,"message":"User not found"}`, http.StatusNotFound)
	}))
	defer notFound.Close()
	cleanup := client.SetFxBaseURLForTesting(notFound.URL)
	code, _, _ := runCLI(t, "circle", "suggest", "ghost")
	cleanup()
	if code != 1 {
		t.Errorf("missing seed: exit = %d, want 1", code)
	}

	// Seed resolves but both lanes are empty -> exit 0, JSON "[]".
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.ToLower(r.URL.Path)
		if strings.HasSuffix(path, "/following") || strings.HasSuffix(path, "/statuses") {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "results": []map[string]any{}})
			return
		}
		_ = json.NewEncoder(w).Encode(fxProfileJSON("seed", 1, ""))
	}))
	defer empty.Close()
	cleanup2 := client.SetFxBaseURLForTesting(empty.URL)
	defer cleanup2()
	code, out, _ := runCLI(t, "circle", "suggest", "seed", "--json")
	if code != 0 {
		t.Fatalf("empty: exit = %d, want 0", code)
	}
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty --json = %q, want []", out)
	}
}

// TestCircleSuggest_SingleLaneDegradation: a failing following lane degrades
// with a stderr warning while the retweet lane still yields candidates.
func TestCircleSuggest_SingleLaneDegradation(t *testing.T) {
	home := tempHome(t)
	writeConfig(t, home)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		path := strings.ToLower(r.URL.Path)
		switch {
		case strings.HasSuffix(path, "/following"):
			http.Error(w, `{"code":500,"message":"boom"}`, http.StatusInternalServerError)
		case strings.HasSuffix(path, "/statuses"):
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "results": []map[string]any{
				fxRetweet("21", "onlyrt"),
			}})
		default:
			_ = json.NewEncoder(w).Encode(fxProfileJSON("seed", 1, ""))
		}
	}))
	defer srv.Close()
	cleanup := client.SetFxBaseURLForTesting(srv.URL)
	defer cleanup()

	code, out, errOut := runCLI(t, "circle", "suggest", "seed", "--json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (stderr %q)", code, errOut)
	}
	if !strings.Contains(errOut, "warning: circle suggest") {
		t.Errorf("stderr = %q, want a degradation warning", errOut)
	}
	if !strings.Contains(out, "onlyrt") {
		t.Errorf("out = %q, want the retweet candidate", out)
	}
}
