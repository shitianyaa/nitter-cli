package nitter_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/sdk"
)

// The structs in models.go/page.go are the NDJSON data contract: their JSON
// keys are frozen (additive-only evolution). These tests pin the exact
// marshaled shape.

// marshalNDJSON encodes like the NDJSON pipeline does (jsonx semantics): one
// compact line with HTML escaping disabled — media URLs carry query strings
// ("name=orig") whose & must survive raw into the output lines. Plain
// json.Marshal would render it as \u0026, so the contract is pinned against
// the encoding the pipeline actually uses.
func marshalNDJSON(t *testing.T, v any) string {
	t.Helper()
	var sb strings.Builder
	enc := json.NewEncoder(&sb)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("Encode(%T) = error %v", v, err)
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

func TestTweetJSONShapeIsTheDataContract(t *testing.T) {
	published := time.Date(2026, 7, 27, 9, 9, 40, 0, time.UTC)
	tw := nitter.Tweet{
		ID:   "2081668333762687236",
		URL:  "https://x.com/NASA/status/2081668333762687236",
		Text: "line one\nline two",
		Author: nitter.Author{
			Handle:    "NASA",
			Name:      "NASA",
			AvatarURL: "https://pbs.twimg.com/profile_images/x_normal.jpg",
		},
		PublishedAt: published,
		Media: []nitter.Media{
			{Type: "image", URL: "https://pbs.twimg.com/media/abc.jpg?format=jpg&name=orig", Width: 1200, Height: 800},
			{Type: "video", URL: "https://video.twimg.com/vid/abc.mp4"},
		},
		IsRetweet:  true,
		RepostedBy: "nasa",
		ReplyTo:    "esa",
		Quote: &nitter.Quoted{
			ID:   "123",
			URL:  "https://x.com/esa/status/123",
			Text: "quoted text",
			Author: nitter.Author{
				Handle: "esa",
				Name:   "ESA",
			},
		},
	}

	got := marshalNDJSON(t, tw)
	want := `{"id":"2081668333762687236","url":"https://x.com/NASA/status/2081668333762687236",` +
		`"text":"line one\nline two",` +
		`"author":{"handle":"NASA","name":"NASA","avatar_url":"https://pbs.twimg.com/profile_images/x_normal.jpg"},` +
		`"published_at":"2026-07-27T09:09:40Z",` +
		`"media":[{"type":"image","url":"https://pbs.twimg.com/media/abc.jpg?format=jpg&name=orig","width":1200,"height":800},` +
		`{"type":"video","url":"https://video.twimg.com/vid/abc.mp4","width":0,"height":0}],` +
		`"is_retweet":true,"reposted_by":"nasa","reply_to":"esa",` +
		`"quote":{"id":"123","url":"https://x.com/esa/status/123","text":"quoted text",` +
		`"author":{"handle":"esa","name":"ESA","avatar_url":""}}}`
	if got != want {
		t.Errorf("Tweet JSON mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestTweetZeroValueMarshalsEveryContractKey(t *testing.T) {
	// The NDJSON shape must be stable regardless of content: every field is
	// always present (no omitempty), so consumers can rely on key presence.
	b, err := json.Marshal(nitter.Tweet{})
	if err != nil {
		t.Fatalf("Marshal(zero Tweet) = error %v", err)
	}
	got := string(b)
	want := `{"id":"","url":"","text":"","author":{"handle":"","name":"","avatar_url":""},` +
		`"published_at":"0001-01-01T00:00:00Z","media":null,"is_retweet":false,` +
		`"reposted_by":"","reply_to":"","quote":null}`
	if got != want {
		t.Errorf("zero Tweet JSON mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestPublishedAtMarshalsRFC3339UTC(t *testing.T) {
	// Producers store UTC (spec section 5); the marshaled form must be the
	// RFC3339 UTC rendering.
	tw := nitter.Tweet{PublishedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	b, err := json.Marshal(tw)
	if err != nil {
		t.Fatalf("Marshal = error %v", err)
	}
	if !strings.Contains(string(b), `"published_at":"2026-01-02T03:04:05Z"`) {
		t.Errorf("PublishedAt = %s, want an RFC3339 UTC string", b)
	}
}

func TestPageJSONShapeIsTheDataContract(t *testing.T) {
	page := nitter.Page[nitter.Tweet]{
		Items:      []nitter.Tweet{{ID: "1"}},
		NextCursor: "cursor-42",
	}
	b, err := json.Marshal(page)
	if err != nil {
		t.Fatalf("Marshal(Page) = error %v", err)
	}
	got := string(b)
	want := `{"items":[{"id":"1","url":"","text":"","author":{"handle":"","name":"","avatar_url":""},` +
		`"published_at":"0001-01-01T00:00:00Z","media":null,"is_retweet":false,` +
		`"reposted_by":"","reply_to":"","quote":null}],"next_cursor":"cursor-42"}`
	if got != want {
		t.Errorf("Page JSON mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestInstanceReportJSONShapeIsTheDataContract(t *testing.T) {
	report := nitter.InstanceReport{
		URL:      "http://nitter:8080",
		RSS:      nitter.Probe{OK: true},
		UserHTML: nitter.Probe{OK: true},
		Search:   nitter.Probe{OK: false, Status: 404, Err: "instance returned HTTP 404"},
		List:     nitter.Probe{},
		Latency:  1500 * time.Millisecond,
	}
	b, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal(InstanceReport) = error %v", err)
	}
	got := string(b)
	want := `{"url":"http://nitter:8080",` +
		`"rss":{"ok":true,"status":0,"err":""},` +
		`"user_html":{"ok":true,"status":0,"err":""},` +
		`"search":{"ok":false,"status":404,"err":"instance returned HTTP 404"},` +
		`"list":{"ok":false,"status":0,"err":""},` +
		`"latency":1500000000}`
	if got != want {
		t.Errorf("InstanceReport JSON mismatch:\n got %s\nwant %s", got, want)
	}
}

func TestProfileJSONShapeIsTheDataContract(t *testing.T) {
	p := nitter.Profile{
		ID:             "44196397",
		Handle:         "elonmusk",
		Name:           "Elon Musk",
		Bio:            "technoking",
		FollowersCount: 200000000,
		FollowingCount: 500,
		TweetsCount:    50000,
		MediaCount:     3000,
		AvatarURL:      "https://pbs.twimg.com/profile_images/avatar.jpg",
		BannerURL:      "https://pbs.twimg.com/profile_banners/banner.jpg",
		IsProtected:    false,
	}

	got := marshalNDJSON(t, p)
	want := `{"id":"44196397","handle":"elonmusk","name":"Elon Musk","bio":"technoking",` +
		`"followers_count":200000000,"following_count":500,"tweets_count":50000,"media_count":3000,` +
		`"avatar_url":"https://pbs.twimg.com/profile_images/avatar.jpg",` +
		`"banner_url":"https://pbs.twimg.com/profile_banners/banner.jpg",` +
		`"is_protected":false}`
	if got != want {
		t.Errorf("Profile JSON mismatch:\n got %s\nwant %s", got, want)
	}

	var decoded nitter.Profile
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("Unmarshal(Profile) = error %v", err)
	}
	if decoded != p {
		t.Errorf("Profile roundtrip mismatch:\n got %+v\nwant %+v", decoded, p)
	}
}

func TestProfileZeroValueMarshalsEveryContractKey(t *testing.T) {
	b, err := json.Marshal(nitter.Profile{})
	if err != nil {
		t.Fatalf("Marshal(zero Profile) = error %v", err)
	}
	got := string(b)
	want := `{"id":"","handle":"","name":"","bio":"","followers_count":0,"following_count":0,"tweets_count":0,"media_count":0,"avatar_url":"","banner_url":"","is_protected":false}`
	if got != want {
		t.Errorf("zero Profile JSON mismatch:\n got %s\nwant %s", got, want)
	}

	var decoded nitter.Profile
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal(zero Profile) = error %v", err)
	}
	if decoded != (nitter.Profile{}) {
		t.Errorf("zero Profile unmarshal mismatch:\n got %+v\nwant zero", decoded)
	}
}

func TestConversationJSONShapeIsTheDataContract(t *testing.T) {
	published := time.Date(2026, 7, 27, 9, 9, 40, 0, time.UTC)
	conv := nitter.Conversation{
		Status: nitter.Tweet{
			ID:          "100",
			URL:         "https://x.com/user/status/100",
			Text:        "main status",
			PublishedAt: published,
		},
		Thread: []nitter.Tweet{
			{
				ID:          "99",
				URL:         "https://x.com/user/status/99",
				Text:        "parent status",
				PublishedAt: published,
			},
		},
		Replies: []nitter.Tweet{
			{
				ID:          "101",
				URL:         "https://x.com/replyer/status/101",
				Text:        "reply status",
				PublishedAt: published,
			},
		},
		Cursor: "cursor_123",
	}

	got := marshalNDJSON(t, conv)
	var decoded nitter.Conversation
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("Unmarshal(Conversation) = error %v", err)
	}
	if decoded.Status.ID != "100" || decoded.Status.Text != "main status" {
		t.Errorf("decoded.Status = %+v, want ID 100", decoded.Status)
	}
	if len(decoded.Thread) != 1 || decoded.Thread[0].ID != "99" {
		t.Errorf("decoded.Thread = %+v, want 1 item with ID 99", decoded.Thread)
	}
	if len(decoded.Replies) != 1 || decoded.Replies[0].ID != "101" {
		t.Errorf("decoded.Replies = %+v, want 1 item with ID 101", decoded.Replies)
	}
	if decoded.Cursor != "cursor_123" {
		t.Errorf("decoded.Cursor = %q, want %q", decoded.Cursor, "cursor_123")
	}
}

func TestConversationZeroValueMarshalsEveryContractKey(t *testing.T) {
	b, err := json.Marshal(nitter.Conversation{})
	if err != nil {
		t.Fatalf("Marshal(zero Conversation) = error %v", err)
	}
	got := string(b)
	for _, key := range []string{`"status":`, `"thread":null`, `"replies":null`, `"cursor":""`} {
		if !strings.Contains(got, key) {
			t.Errorf("zero Conversation missing expected key pattern %q: %s", key, got)
		}
	}

	var decoded nitter.Conversation
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("Unmarshal(zero Conversation) = error %v", err)
	}
	if decoded.Cursor != "" || decoded.Thread != nil || decoded.Replies != nil {
		t.Errorf("decoded zero Conversation mismatch: %+v", decoded)
	}
}

func TestTrendJSONShapeIsTheDataContract(t *testing.T) {
	trend := nitter.Trend{
		Name:          "#GoLang",
		Rank:          1,
		Context:       "Technology · Trending",
		TweetCount:    15200,
		GroupedTopics: []string{"Programming", "Software"},
	}

	got := marshalNDJSON(t, trend)
	want := `{"name":"#GoLang","rank":1,"context":"Technology · Trending","tweet_count":15200,"grouped_topics":["Programming","Software"]}`
	if got != want {
		t.Errorf("Trend JSON mismatch:\n got %s\nwant %s", got, want)
	}

	var decoded nitter.Trend
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("Unmarshal(Trend) = error %v", err)
	}
	if decoded.Name != "#GoLang" || decoded.Rank != 1 || decoded.Context != "Technology · Trending" || decoded.TweetCount != 15200 || len(decoded.GroupedTopics) != 2 {
		t.Errorf("decoded Trend mismatch: %+v", decoded)
	}
}

func TestTrendZeroValueMarshalsEveryContractKey(t *testing.T) {
	b, err := json.Marshal(nitter.Trend{})
	if err != nil {
		t.Fatalf("Marshal(zero Trend) = error %v", err)
	}
	got := string(b)
	want := `{"name":"","rank":0,"context":"","tweet_count":0,"grouped_topics":null}`
	if got != want {
		t.Errorf("zero Trend JSON mismatch:\n got %s\nwant %s", got, want)
	}
}
