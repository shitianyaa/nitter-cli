package fxtwitter_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/shitianyaa/nitter-cli/internal/fxtwitter"
	sdk "github.com/shitianyaa/nitter-cli/sdk"
)

func TestTimelineRoutingAndRetweetGuard(t *testing.T) {
	var mu sync.Mutex
	var requestedURLs []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestedURLs = append(requestedURLs, r.URL.String())
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasPrefix(r.URL.Path, "/2/profile/user/media"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"results": []map[string]any{
					{
						"id":   "101",
						"text": "Media tweet",
						"media": map[string]any{
							"all": []map[string]any{
								{"type": "photo", "url": "https://pbs.twimg.com/media/101.jpg"},
							},
						},
					},
				},
				"cursor": map[string]any{"bottom": "cur_media_end"},
			})
		case strings.HasPrefix(r.URL.Path, "/2/profile/user/statuses"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"results": []map[string]any{
					{
						"id":   "1",
						"text": "Plain text tweet",
					},
					{
						"id":          "2",
						"text":        "Retweet with media",
						"is_retweet":  true,
						"reposted_by": map[string]any{"name": "User Name", "screen_name": "user"},
						"media": map[string]any{
							"all": []map[string]any{
								{"type": "photo", "url": "https://pbs.twimg.com/media/rt.jpg"},
							},
						},
					},
					{
						"id":          "3",
						"text":        "Retweet plain text",
						"is_retweet":  true,
						"reposted_by": "user",
					},
					{
						"id":   "4",
						"text": "Own photo tweet",
						"media": map[string]any{
							"all": []map[string]any{
								{"type": "photo", "url": "https://pbs.twimg.com/media/own.jpg"},
							},
						},
					},
				},
				"cursor": "cur_status_end",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	// 1. Plain text + filter reposts: calls /statuses, filters out tweet 2 and 3
	mu.Lock()
	requestedURLs = nil
	mu.Unlock()
	tweets, cursor, err := client.FetchUserTimeline(ctx, "user", 10, "", 1, false, true)
	if err != nil {
		t.Fatalf("FetchUserTimeline error: %v", err)
	}
	if len(tweets) != 2 || tweets[0].ID != "1" || tweets[1].ID != "4" {
		t.Errorf("FetchUserTimeline plain+filter_reposts got %d tweets, want [1, 4]", len(tweets))
	}
	if cursor != "cur_status_end" {
		t.Errorf("cursor = %q, want cur_status_end", cursor)
	}
	if !strings.Contains(requestedURLs[0], "/2/profile/user/statuses") {
		t.Errorf("expected /statuses endpoint, got %s", requestedURLs[0])
	}

	// 2. Plain text + keep reposts: calls /statuses, keeps all 4 tweets
	mu.Lock()
	requestedURLs = nil
	mu.Unlock()
	tweets, _, err = client.FetchUserTimeline(ctx, "user", 10, "", 1, false, false)
	if err != nil {
		t.Fatalf("FetchUserTimeline error: %v", err)
	}
	if len(tweets) != 4 {
		t.Errorf("FetchUserTimeline plain+keep_reposts got %d tweets, want 4", len(tweets))
	}
	if !tweets[1].IsRetweet || tweets[1].RepostedBy != "User Name" {
		t.Errorf("tweet 2 is_retweet mismatch: is_retweet=%v reposted_by=%q", tweets[1].IsRetweet, tweets[1].RepostedBy)
	}

	// 3. Skip plain text + filter reposts: routes directly to /media
	mu.Lock()
	requestedURLs = nil
	mu.Unlock()
	tweets, _, err = client.FetchUserTimeline(ctx, "user", 10, "", 1, true, true)
	if err != nil {
		t.Fatalf("FetchUserTimeline error: %v", err)
	}
	if len(requestedURLs) != 1 || !strings.Contains(requestedURLs[0], "/2/profile/user/media") {
		t.Errorf("expected /media endpoint, got %v", requestedURLs)
	}
	if len(tweets) != 1 || tweets[0].ID != "101" {
		t.Errorf("expected 1 tweet from media endpoint, got %d", len(tweets))
	}

	// 4. RETWEET GUARD: skip plain text + keep reposts (filterReposts=false)
	// MUST call /statuses (NOT /media) and filter plain text locally, preserving retweet with media!
	mu.Lock()
	requestedURLs = nil
	mu.Unlock()
	tweets, _, err = client.FetchUserTimeline(ctx, "user", 10, "", 1, true, false)
	if err != nil {
		t.Fatalf("FetchUserTimeline error: %v", err)
	}
	if len(requestedURLs) != 1 || !strings.Contains(requestedURLs[0], "/2/profile/user/statuses") {
		t.Errorf("expected /statuses endpoint under Retweet Guard, got %v", requestedURLs)
	}
	if len(tweets) != 2 {
		t.Fatalf("expected 2 tweets (retweet with media and own photo), got %d", len(tweets))
	}
	if tweets[0].ID != "2" || !tweets[0].IsRetweet {
		t.Errorf("tweet[0] expected ID 2 retweet, got %+v", tweets[0])
	}
	if tweets[1].ID != "4" || tweets[1].IsRetweet {
		t.Errorf("tweet[1] expected ID 4 own tweet, got %+v", tweets[1])
	}
}

func TestTimelinePaginationAndMaxPages(t *testing.T) {
	pageCounter := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pageCounter++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"results": []map[string]any{
				{
					"id":   string(rune('0' + pageCounter)),
					"text": "Tweet on page",
					"media": map[string]any{
						"all": []map[string]any{
							{"type": "photo", "url": "https://pbs.twimg.com/media/pic.jpg"},
						},
					},
				},
			},
			"cursor": map[string]any{
				"bottom": "cur_page_" + string(rune('0'+pageCounter+1)),
			},
		})
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	// Ask for 5 items, but maxPages=2
	tweets, cursor, err := client.FetchUserTimeline(ctx, "user", 5, "", 2, false, true)
	if err != nil {
		t.Fatalf("FetchUserTimeline pagination error: %v", err)
	}
	if pageCounter != 2 {
		t.Errorf("expected exactly 2 pages fetched, got %d", pageCounter)
	}
	if len(tweets) != 2 {
		t.Errorf("expected 2 tweets accumulated, got %d", len(tweets))
	}
	if cursor != "cur_page_3" {
		t.Errorf("expected cursor cur_page_3, got %q", cursor)
	}
}

// TestFetchUserMediaCursorCycleGuard mirrors the timeline cycle case for the
// media endpoint: the chain B -> A -> B revisits cur_B from page 1, which is
// neither an empty cursor nor an immediate self-repeat, so only the
// seenCursors guard can stop the loop (without it the run would use the full
// 5-page budget). The single-page media test cannot reach this code.
//
// The bounded budget is deliberate: the production unbounded form (limit <= 0
// becomes count = MaxInt with maxPages = -1) is exercised elsewhere, but here a
// missing guard would spin forever instead of failing the assertions — a
// bounded budget turns that into a readable failure.
func TestFetchUserMediaCursorCycleGuard(t *testing.T) {
	reqCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "application/json")
		nextCursor := "cur_A"
		switch reqCount {
		case 1:
			nextCursor = "cur_B"
		case 2:
			nextCursor = "cur_A"
		case 3:
			nextCursor = "cur_B"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"tweets": []map[string]any{
				{"id": fmt.Sprintf("%d", reqCount), "text": "media tweet"},
			},
			"cursor": nextCursor,
		})
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	tweets, cur, err := client.FetchUserMedia(context.Background(), "artist", 100, "", 5)
	if err != nil {
		t.Fatalf("FetchUserMedia failed: %v", err)
	}
	if reqCount != 3 {
		t.Errorf("reqCount = %d, want the loop to stop on the revisited cursor (3 requests, not 5)", reqCount)
	}
	if len(tweets) != 3 {
		t.Errorf("got %d tweets, want 3 (one per fetched page)", len(tweets))
	}
	// Companion assertion: a stalled chain reports no continuation. It does not
	// discriminate between the guards (an immediate self-repeat also yields
	// ""); reqCount and len(tweets) above are the discriminating assertions.
	if cur != "" {
		t.Errorf("cursor = %q, want empty: a stalled chain has no continuation", cur)
	}
}

func TestUserMediaEndpointAndTweetConversion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/2/profile/artist/media") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"tweets": []map[string]any{
				{
					"id":         "1001",
					"url":        "https://x.com/artist/status/1001",
					"text":       "Art gallery",
					"created_at": "Sat, 05 Jul 2026 09:09:40 GMT",
					"author": map[string]any{
						"screen_name": "artist",
						"name":        "Famous Artist",
						"avatar_url":  "https://pbs.twimg.com/avatar.jpg",
					},
					"media": map[string]any{
						"all": []map[string]any{
							{
								"type":   "photo",
								"url":    "https://pbs.twimg.com/media/art.jpg",
								"width":  1920,
								"height": 1080,
							},
							{
								"type":   "video",
								"url":    "https://video.twimg.com/vid.mp4",
								"width":  1280,
								"height": 720,
							},
							{
								"type":   "animated_gif",
								"url":    "https://video.twimg.com/gif.mp4",
								"width":  500,
								"height": 500,
							},
						},
					},
				},
			},
			"cursor": "next_media_cur",
		})
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	tweets, nextCur, err := client.FetchUserMedia(ctx, "artist", 10, "", 1)
	if err != nil {
		t.Fatalf("FetchUserMedia failed: %v", err)
	}
	if len(tweets) != 1 {
		t.Fatalf("expected 1 tweet, got %d", len(tweets))
	}
	if nextCur != "next_media_cur" {
		t.Errorf("cursor mismatch: got %q, want next_media_cur", nextCur)
	}

	tw := tweets[0]
	if tw.ID != "1001" {
		t.Errorf("ID = %q, want 1001", tw.ID)
	}
	if tw.URL != "https://x.com/artist/status/1001" {
		t.Errorf("URL = %q, want https://x.com/artist/status/1001", tw.URL)
	}
	if tw.Text != "Art gallery" {
		t.Errorf("Text = %q, want Art gallery", tw.Text)
	}
	if tw.Author.Handle != "artist" || tw.Author.Name != "Famous Artist" || tw.Author.AvatarURL != "https://pbs.twimg.com/avatar.jpg" {
		t.Errorf("Author mismatch: %+v", tw.Author)
	}
	expectedTime := time.Date(2026, 7, 5, 9, 9, 40, 0, time.UTC)
	if !tw.PublishedAt.Equal(expectedTime) {
		t.Errorf("PublishedAt = %v, want %v", tw.PublishedAt, expectedTime)
	}
	if len(tw.Media) != 3 {
		t.Fatalf("Media count = %d, want 3", len(tw.Media))
	}
	if tw.Media[0].Type != "image" || tw.Media[0].Width != 1920 || tw.Media[0].Height != 1080 {
		t.Errorf("Media[0] mismatch: %+v", tw.Media[0])
	}
	if tw.Media[1].Type != "video" || tw.Media[1].Width != 1280 || tw.Media[1].Height != 720 {
		t.Errorf("Media[1] mismatch: %+v", tw.Media[1])
	}
	if tw.Media[2].Type != "gif" || tw.Media[2].Width != 500 || tw.Media[2].Height != 500 {
		t.Errorf("Media[2] mismatch: %+v", tw.Media[2])
	}
}

func TestProfileAndFollowingConversion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.EqualFold(r.URL.Path, "/2/profile/nasa"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"user": map[string]any{
					"id_str":          "11348282",
					"screen_name":     "NASA",
					"name":            "NASA",
					"description":     "Exploring the universe and our home planet.",
					"followers_count": 80000000,
					"friends_count":   300,
					"statuses_count":  75000,
					"media_count":     12000,
					"avatar_url":      "https://pbs.twimg.com/nasa_avatar.jpg",
					"banner_url":      "https://pbs.twimg.com/nasa_banner.jpg",
					"protected":       false,
				},
			})
		case strings.EqualFold(r.URL.Path, "/2/profile/nasa/following"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"users": []map[string]any{
					{
						"id":              "1",
						"username":        "ESA",
						"name":            "European Space Agency",
						"bio":             "Europe's gateway to space.",
						"followers_count": 2000000,
						"following_count": 500,
						"tweets_count":    40000,
						"media_count":     5000,
						"avatar_url":      "https://pbs.twimg.com/esa.jpg",
						"banner_url":      "https://pbs.twimg.com/esa_banner.jpg",
						"is_protected":    false,
					},
					{
						"id":              "2",
						"username":        "JAXA",
						"name":            "JAXA",
						"bio":             "Japan Aerospace Exploration Agency",
						"followers_count": 1000000,
						"following_count": 200,
						"tweets_count":    20000,
						"media_count":     3000,
						"avatar_url":      "https://pbs.twimg.com/jaxa.jpg",
						"banner_url":      "https://pbs.twimg.com/jaxa_banner.jpg",
						"is_protected":    true,
					},
				},
				"cursor": map[string]any{"next": "cur_following_2"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	// 1. FetchUserProfile
	prof, err := client.FetchUserProfile(ctx, "NASA")
	if err != nil {
		t.Fatalf("FetchUserProfile failed: %v", err)
	}
	if prof.ID != "11348282" {
		t.Errorf("prof.ID = %q, want 11348282", prof.ID)
	}
	if prof.Handle != "NASA" {
		t.Errorf("prof.Handle = %q, want NASA", prof.Handle)
	}
	if prof.Name != "NASA" {
		t.Errorf("prof.Name = %q, want NASA", prof.Name)
	}
	if prof.Bio != "Exploring the universe and our home planet." {
		t.Errorf("prof.Bio = %q", prof.Bio)
	}
	if prof.FollowersCount != 80000000 || prof.FollowingCount != 300 || prof.TweetsCount != 75000 || prof.MediaCount != 12000 {
		t.Errorf("counts mismatch: followers=%d following=%d tweets=%d media=%d",
			prof.FollowersCount, prof.FollowingCount, prof.TweetsCount, prof.MediaCount)
	}
	if prof.AvatarURL != "https://pbs.twimg.com/nasa_avatar.jpg" || prof.BannerURL != "https://pbs.twimg.com/nasa_banner.jpg" {
		t.Errorf("URLs mismatch: avatar=%q banner=%q", prof.AvatarURL, prof.BannerURL)
	}
	if prof.IsProtected {
		t.Errorf("IsProtected = true, want false")
	}

	// 2. FetchUserFollowing
	following, cur, err := client.FetchUserFollowing(ctx, "NASA", 10, "")
	if err != nil {
		t.Fatalf("FetchUserFollowing failed: %v", err)
	}
	if len(following) != 2 {
		t.Fatalf("following count = %d, want 2", len(following))
	}
	if cur != "cur_following_2" {
		t.Errorf("cursor = %q, want cur_following_2", cur)
	}
	if following[0].Handle != "ESA" || following[0].FollowersCount != 2000000 {
		t.Errorf("following[0] mismatch: %+v", following[0])
	}
	if following[1].Handle != "JAXA" || !following[1].IsProtected {
		t.Errorf("following[1] mismatch: %+v", following[1])
	}
}

func TestConversationThreadAndRepliesMapping(t *testing.T) {
	var requestedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/2/conversation/200") {
			http.NotFound(w, r)
			return
		}
		requestedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"tweet": map[string]any{
				"id":   "200",
				"text": "Focal status tweet",
				"author": map[string]any{
					"screen_name": "root_user",
					"name":        "Root User",
				},
			},
			"thread": []map[string]any{
				{
					"id":   "199",
					"text": "Parent status in thread",
					"author": map[string]any{
						"screen_name": "parent_user",
					},
				},
			},
			"replies": []map[string]any{
				{
					"id":   "201",
					"text": "First reply",
					"author": map[string]any{
						"screen_name": "replier_1",
					},
				},
				{
					"id":   "202",
					"text": "Second reply",
					"author": map[string]any{
						"screen_name": "replier_2",
					},
				},
			},
			"cursor": "conv_next_cursor",
		})
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	conv, err := client.FetchConversation(ctx, "200", "Relevance", "prev_cur")
	if err != nil {
		t.Fatalf("FetchConversation failed: %v", err)
	}

	if !strings.Contains(requestedQuery, "ranking_mode=Relevance") || !strings.Contains(requestedQuery, "cursor=prev_cur") {
		t.Errorf("unexpected query string: %s", requestedQuery)
	}

	if conv.Status.ID != "200" || conv.Status.Text != "Focal status tweet" {
		t.Errorf("conv.Status mismatch: %+v", conv.Status)
	}
	if len(conv.Thread) != 1 || conv.Thread[0].ID != "199" {
		t.Errorf("conv.Thread mismatch: %+v", conv.Thread)
	}
	if len(conv.Replies) != 2 || conv.Replies[0].ID != "201" || conv.Replies[1].ID != "202" {
		t.Errorf("conv.Replies mismatch: %+v", conv.Replies)
	}
	if conv.Cursor != "conv_next_cursor" {
		t.Errorf("conv.Cursor = %q, want conv_next_cursor", conv.Cursor)
	}
}

func TestNotFoundAndSafeSearchMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/2/search" && strings.Contains(r.URL.RawQuery, "q=sensitive"):
			// SafeSearch HTTP 404
			http.Error(w, `{"code": 404, "message": "SafeSearch restricted"}`, http.StatusNotFound)
		case r.URL.Path == "/2/search" && strings.Contains(r.URL.RawQuery, "q=nsfw_code_404"):
			// SafeSearch HTTP 200 with code: 404 in JSON
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    404,
				"message": "SafeSearch restricted",
			})
		case r.URL.Path == "/2/profile/nonexistent":
			http.Error(w, `{"code": 404, "message": "User not found"}`, http.StatusNotFound)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	// 1. SafeSearch via HTTP 404
	_, _, err := client.SearchTweets(ctx, "sensitive", 10, "", "latest")
	if err == nil {
		t.Fatal("expected error on SafeSearch 404, got nil")
	}
	var terr *sdk.Error
	if !errors.As(err, &terr) || terr.Kind != sdk.KindNotFound {
		t.Errorf("expected KindNotFound for SafeSearch 404, got %v (%T)", err, err)
	}
	if !fxtwitter.IsNotFound(err) {
		t.Errorf("fxtwitter.IsNotFound(err) = false, want true")
	}

	// 2. SafeSearch via JSON body code 404 (HTTP 200)
	_, _, err = client.SearchTweets(ctx, "nsfw_code_404", 10, "", "latest")
	if err == nil {
		t.Fatal("expected error on SafeSearch JSON code 404, got nil")
	}
	if !errors.As(err, &terr) || terr.Kind != sdk.KindNotFound {
		t.Errorf("expected KindNotFound for JSON code 404, got %v (%T)", err, err)
	}
	if !fxtwitter.IsNotFound(err) {
		t.Errorf("fxtwitter.IsNotFound(err) = false, want true")
	}

	// 3. Profile 404
	_, err = client.FetchUserProfile(ctx, "nonexistent")
	if err == nil {
		t.Fatal("expected error on Profile 404, got nil")
	}
	if !errors.As(err, &terr) || terr.Kind != sdk.KindNotFound {
		t.Errorf("expected KindNotFound for Profile 404, got %v (%T)", err, err)
	}
	if !fxtwitter.IsNotFound(err) {
		t.Errorf("fxtwitter.IsNotFound(err) = false, want true")
	}
}

func TestGracefulPartialReturnsOnStalledCursorsAndExhaustion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/2/profile/stalled/statuses":
			// Stalled cursor: returns same cursor as passed
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"results": []map[string]any{
					{"id": "1", "text": "Stalled tweet"},
				},
				"cursor": map[string]any{"bottom": "same_cur"},
			})
		case r.URL.Path == "/2/profile/exhausted/statuses":
			// Results exhausted: empty results
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    200,
				"results": []map[string]any{},
				"cursor":  map[string]any{"bottom": "new_cur"},
			})
		case r.URL.Path == "/2/search":
			// Stalled cursor in search
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"results": []map[string]any{
					{"id": "99", "text": "Search result"},
				},
				"cursor": "same_search_cur",
			})
		case r.URL.Path == "/2/profile/user/following":
			// Exhausted following list
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":   200,
				"users":  []map[string]any{},
				"cursor": "cur_following",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	// 1. Timeline stalled cursor: returns partial tweets and empty cursor, nil error
	tweets, cur, err := client.FetchUserTimeline(ctx, "stalled", 10, "same_cur", 5, false, false)
	if err != nil {
		t.Fatalf("expected nil error on stalled cursor, got %v", err)
	}
	if len(tweets) != 1 || tweets[0].ID != "1" {
		t.Errorf("expected 1 tweet returned, got %d", len(tweets))
	}
	if cur != "" {
		t.Errorf("expected empty cursor on stalled cursor, got %q", cur)
	}

	// 2. Timeline exhausted results: returns partial tweets (empty) and empty cursor, nil error
	tweets, cur, err = client.FetchUserTimeline(ctx, "exhausted", 10, "", 5, false, false)
	if err != nil {
		t.Fatalf("expected nil error on exhausted results, got %v", err)
	}
	if len(tweets) != 0 {
		t.Errorf("expected 0 tweets returned, got %d", len(tweets))
	}
	if cur != "" {
		t.Errorf("expected empty cursor on exhausted results, got %q", cur)
	}

	// 3. Search stalled cursor: returns results and empty cursor, nil error
	tweets, cur, err = client.SearchTweets(ctx, "test", 10, "same_search_cur", "latest")
	if err != nil {
		t.Fatalf("expected nil error on search stalled cursor, got %v", err)
	}
	if len(tweets) != 1 || tweets[0].ID != "99" {
		t.Errorf("expected 1 tweet returned, got %d", len(tweets))
	}
	if cur != "" {
		t.Errorf("expected empty cursor on search stalled cursor, got %q", cur)
	}

	// 4. Following exhausted: returns empty list and empty cursor, nil error
	profs, cur, err := client.FetchUserFollowing(ctx, "user", 10, "")
	if err != nil {
		t.Fatalf("expected nil error on following exhausted, got %v", err)
	}
	if len(profs) != 0 {
		t.Errorf("expected 0 profiles returned, got %d", len(profs))
	}
}

func TestEmptyInputsValidation(t *testing.T) {
	client := fxtwitter.NewClient()
	ctx := context.Background()

	// Empty handle in timeline returns empty without error
	tw, cur, err := client.FetchUserTimeline(ctx, "", 10, "", 1, false, false)
	if err != nil || len(tw) != 0 || cur != "" {
		t.Errorf("expected empty return on empty handle, got tw=%v cur=%q err=%v", tw, cur, err)
	}

	// Zero count in timeline returns empty without error
	tw, cur, err = client.FetchUserTimeline(ctx, "user", 0, "", 1, false, false)
	if err != nil || len(tw) != 0 || cur != "" {
		t.Errorf("expected empty return on zero count, got tw=%v cur=%q err=%v", tw, cur, err)
	}

	// Empty query in search returns empty without error
	tw, cur, err = client.SearchTweets(ctx, "   ", 10, "", "latest")
	if err != nil || len(tw) != 0 || cur != "" {
		t.Errorf("expected empty return on blank query, got tw=%v cur=%q err=%v", tw, cur, err)
	}

	// Empty handle in profile returns KindInvalidArg
	_, err = client.FetchUserProfile(ctx, "")
	if err == nil {
		t.Error("expected error on empty profile handle, got nil")
	}
	var terr *sdk.Error
	if !errors.As(err, &terr) || terr.Kind != sdk.KindInvalidArg {
		t.Errorf("expected KindInvalidArg for empty profile handle, got %v", err)
	}

	// Empty statusID in conversation returns KindInvalidArg
	_, err = client.FetchConversation(ctx, "", "", "")
	if err == nil {
		t.Error("expected error on empty conversation statusID, got nil")
	}
	if !errors.As(err, &terr) || terr.Kind != sdk.KindInvalidArg {
		t.Errorf("expected KindInvalidArg for empty conversation statusID, got %v", err)
	}

	// Empty handle in following returns KindInvalidArg
	_, _, err = client.FetchUserFollowing(ctx, "", 10, "")
	if err == nil {
		t.Error("expected error on empty following handle, got nil")
	}
	if !errors.As(err, &terr) || terr.Kind != sdk.KindInvalidArg {
		t.Errorf("expected KindInvalidArg for empty following handle, got %v", err)
	}

	// Empty statusID in FetchStatus returns KindInvalidArg
	_, err = client.FetchStatus(ctx, "")
	if err == nil {
		t.Error("expected error on empty statusID in FetchStatus, got nil")
	}
	if !errors.As(err, &terr) || terr.Kind != sdk.KindInvalidArg {
		t.Errorf("expected KindInvalidArg for empty statusID, got %v", err)
	}

	// Empty statusID in FetchQuotes returns KindInvalidArg
	_, _, err = client.FetchQuotes(ctx, "", 10, "")
	if err == nil {
		t.Error("expected error on empty statusID in FetchQuotes, got nil")
	}
	if !errors.As(err, &terr) || terr.Kind != sdk.KindInvalidArg {
		t.Errorf("expected KindInvalidArg for empty statusID, got %v", err)
	}

	// Empty query in SearchUsers returns KindInvalidArg
	_, err = client.SearchUsers(ctx, "", 10)
	if err == nil {
		t.Error("expected error on empty query in SearchUsers, got nil")
	}
	if !errors.As(err, &terr) || terr.Kind != sdk.KindInvalidArg {
		t.Errorf("expected KindInvalidArg for empty query, got %v", err)
	}
}

func TestFetchStatusSuccessAndNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/2/status/12345":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 200,
				"tweet": map[string]any{
					"id":   "12345",
					"text": "Hello world from Fx status",
					"author": map[string]any{
						"screen_name": "jack",
						"name":        "Jack",
					},
					"media": map[string]any{
						"all": []map[string]any{
							{"type": "photo", "url": "https://pbs.twimg.com/media/pic.jpg"},
						},
					},
				},
			})
		case r.URL.Path == "/2/status/40404":
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    404,
				"message": "Status not found",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	// 1. Success
	tw, err := client.FetchStatus(ctx, "12345")
	if err != nil {
		t.Fatalf("FetchStatus failed: %v", err)
	}
	if tw.ID != "12345" || tw.Text != "Hello world from Fx status" || tw.Author.Handle != "jack" {
		t.Errorf("FetchStatus tweet mismatch: %+v", tw)
	}
	if len(tw.Media) != 1 || tw.Media[0].URL != "https://pbs.twimg.com/media/pic.jpg" {
		t.Errorf("FetchStatus media mismatch: %+v", tw.Media)
	}

	// 2. 404
	_, err = client.FetchStatus(ctx, "40404")
	if err == nil {
		t.Fatal("expected error for 404 status, got nil")
	}
	var terr *sdk.Error
	if !errors.As(err, &terr) || terr.Kind != sdk.KindNotFound {
		t.Errorf("expected KindNotFound for 404 status, got %v", err)
	}
}

func TestFetchQuotes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/2/status/9999/quotes") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"quotes": []map[string]any{
				{
					"id":   "1001",
					"text": "Quote 1",
					"author": map[string]any{
						"screen_name": "quoter1",
					},
				},
				{
					"id":   "1002",
					"text": "Quote 2",
					"author": map[string]any{
						"screen_name": "quoter2",
					},
				},
			},
			"cursor": "next_quotes_cursor",
		})
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	tweets, nextCursor, err := client.FetchQuotes(ctx, "9999", 10, "")
	if err != nil {
		t.Fatalf("FetchQuotes failed: %v", err)
	}
	if len(tweets) != 2 || tweets[0].ID != "1001" || tweets[1].ID != "1002" {
		t.Errorf("FetchQuotes tweets mismatch: %+v", tweets)
	}
	if nextCursor != "next_quotes_cursor" {
		t.Errorf("FetchQuotes cursor = %q, want next_quotes_cursor", nextCursor)
	}
}

func TestFetchTrends(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/trends" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"trends": []map[string]any{
				{
					"name":           "#AI",
					"rank":           1,
					"context":        "Technology · Trending",
					"tweet_count":    42000,
					"grouped_topics": []string{"Tech", "Computing"},
				},
				{
					"name":        "SpaceX",
					"rank":        nil,
					"context":     "Science · Trending",
					"tweet_count": 15000,
				},
				{
					"name": "", // should be skipped
				},
			},
		})
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	trends, err := client.FetchTrends(ctx)
	if err != nil {
		t.Fatalf("FetchTrends failed: %v", err)
	}
	if len(trends) != 2 {
		t.Fatalf("FetchTrends returned %d trends, want 2", len(trends))
	}
	if trends[0].Name != "#AI" || trends[0].Rank != 1 || trends[0].TweetCount != 42000 || len(trends[0].GroupedTopics) != 2 {
		t.Errorf("trends[0] mismatch: %+v", trends[0])
	}
	// Defensively assigns rank 2 for second item
	if trends[1].Name != "SpaceX" || trends[1].Rank != 2 || trends[1].TweetCount != 15000 {
		t.Errorf("trends[1] mismatch: %+v", trends[1])
	}
}

func TestSearchUsers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/2/search/users" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("q") != "artist" {
			http.Error(w, `{"code":400,"message":"bad query"}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"users": []map[string]any{
				{
					"screen_name":     "picasso",
					"name":            "Pablo Picasso",
					"description":     "Cubist painter",
					"followers_count": 50000,
				},
				{
					"screen_name":     "monet",
					"name":            "Claude Monet",
					"description":     "Impressionist painter",
					"followers_count": 30000,
				},
			},
		})
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	users, err := client.SearchUsers(ctx, "artist", 10)
	if err != nil {
		t.Fatalf("SearchUsers failed: %v", err)
	}
	if len(users) != 2 {
		t.Fatalf("SearchUsers returned %d users, want 2", len(users))
	}
	if users[0].Handle != "picasso" || users[0].Name != "Pablo Picasso" || users[0].FollowersCount != 50000 {
		t.Errorf("users[0] mismatch: %+v", users[0])
	}
	if users[1].Handle != "monet" || users[1].Bio != "Impressionist painter" {
		t.Errorf("users[1] mismatch: %+v", users[1])
	}
}

// TestRealFxTwitterTweetCountKeys locks the tweet-count key names actually
// emitted by FxTwitter. Profile and author objects use "statuses" (an int) for
// the tweet count — not "tweets_count"/"statuses_count" — so a RawAuthor
// missing that tag silently reports tweets_count: 0 for every profile.
func TestRealFxTwitterTweetCountKeys(t *testing.T) {
	// Verbatim shape captured from https://api.fxtwitter.com/2/profile/NASA
	// (field order/keys preserved; only the counts are abbreviated).
	const profileBody = `{
		"code": 200,
		"message": "OK",
		"user": {
			"screen_name": "NASA",
			"id": "11348282",
			"followers": 92377960,
			"following": 117,
			"media_count": 28154,
			"statuses": 74322,
			"name": "NASA",
			"description": "Making the seemingly impossible, possible.",
			"protected": false
		}
	}`
	const followingBody = `{
		"code": 200,
		"results": [
			{
				"screen_name": "NASAHubble",
				"id": "14091091",
				"followers": 8902551,
				"following": 44,
				"media_count": 3131,
				"statuses": 8455,
				"name": "Hubble",
				"protected": false
			}
		]
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.EqualFold(r.URL.Path, "/2/profile/nasa"):
			_, _ = w.Write([]byte(profileBody))
		case strings.EqualFold(r.URL.Path, "/2/profile/nasa/following"):
			_, _ = w.Write([]byte(followingBody))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	prof, err := client.FetchUserProfile(ctx, "NASA")
	if err != nil {
		t.Fatalf("FetchUserProfile failed: %v", err)
	}
	if prof.TweetsCount != 74322 {
		t.Errorf("profile TweetsCount = %d, want 74322 (from the \"statuses\" key)", prof.TweetsCount)
	}
	if prof.FollowersCount != 92377960 || prof.FollowingCount != 117 || prof.MediaCount != 28154 {
		t.Errorf("profile counts mismatch: followers=%d following=%d media=%d",
			prof.FollowersCount, prof.FollowingCount, prof.MediaCount)
	}

	following, _, err := client.FetchUserFollowing(ctx, "NASA", 10, "")
	if err != nil {
		t.Fatalf("FetchUserFollowing failed: %v", err)
	}
	if len(following) != 1 {
		t.Fatalf("following count = %d, want 1", len(following))
	}
	if following[0].TweetsCount != 8455 {
		t.Errorf("following[0].TweetsCount = %d, want 8455 (from the \"statuses\" key)", following[0].TweetsCount)
	}
}

// TestSearchTweetsFeedParameter pins the feed contract of the search
// endpoint: "latest" (and the empty default) go out as feed=latest, "top" as
// feed=top, and an unknown value is rejected as invalid_argument BEFORE any
// request — never forwarded upstream and never silently swapped for a
// default.
func TestSearchTweetsFeedParameter(t *testing.T) {
	var mu sync.Mutex
	var queries []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"results":[{"id":"7","url":"https://x.com/user/status/7","text":"hit","author":{"screen_name":"user"}}]}`)
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	ctx := context.Background()

	seen := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), queries...)
	}

	for _, tc := range []struct{ feed, want string }{
		{"latest", "feed=latest"},
		{"", "feed=latest"},
		{"TOP", "feed=top"},
		{" top ", "feed=top"},
	} {
		mu.Lock()
		queries = nil
		mu.Unlock()

		tweets, _, err := client.SearchTweets(ctx, "moon", 10, "", tc.feed)
		if err != nil {
			t.Fatalf("SearchTweets(feed=%q): %v", tc.feed, err)
		}
		if len(tweets) != 1 {
			t.Fatalf("SearchTweets(feed=%q): got %d tweets, want 1", tc.feed, len(tweets))
		}
		got := seen()
		if len(got) != 1 || !strings.Contains(got[0], tc.want) {
			t.Errorf("SearchTweets(feed=%q): queries = %v, want %q", tc.feed, got, tc.want)
		}
	}

	mu.Lock()
	queries = nil
	mu.Unlock()
	_, _, err := client.SearchTweets(ctx, "moon", 10, "", "bogus")
	if err == nil {
		t.Fatal("SearchTweets(feed=bogus) = nil error, want invalid_argument")
	}
	var serr *sdk.Error
	if !errors.As(err, &serr) || serr.Kind != sdk.KindInvalidArg {
		t.Errorf("err = %v (%T), want KindInvalidArg", err, err)
	}
	if got := seen(); len(got) != 0 {
		t.Errorf("queries = %v, want none (the feed is validated before any request)", got)
	}
}

func TestFetchUserTimelineDefaultPagesAndLoopGuard(t *testing.T) {
	ctx := context.Background()

	// 1. Verify that pagesLimit defaults to 5 when maxPages <= 0
	reqCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"tweets": []map[string]any{
				{"id": fmt.Sprintf("%d", reqCount), "text": "tweet"},
			},
			"cursor": fmt.Sprintf("cur_%d", reqCount),
		})
	}))
	defer srv.Close()

	client := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv.URL), fxtwitter.WithHTTPClient(srv.Client()))
	tweets, _, err := client.FetchUserTimeline(ctx, "user", 100, "", 0, false, false)
	if err != nil {
		t.Fatalf("FetchUserTimeline failed: %v", err)
	}
	if reqCount != 5 {
		t.Errorf("reqCount = %d, want 5 pages by default", reqCount)
	}
	if len(tweets) != 5 {
		t.Errorf("got %d tweets, want 5", len(tweets))
	}

	// 2. Verify the seenCursors guard breaks a multi-step cursor cycle: the
	// chain B -> A -> B revisits cur_B from page 1, which is neither an empty
	// cursor nor an immediate self-repeat, so only seenCursors can stop it
	// (without that guard the loop would run to the 5-page budget).
	reqCount2 := 0
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount2++
		w.Header().Set("Content-Type", "application/json")
		nextCursor := "cur_A"
		switch reqCount2 {
		case 1:
			nextCursor = "cur_B"
		case 2:
			nextCursor = "cur_A"
		case 3:
			nextCursor = "cur_B"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"tweets": []map[string]any{
				{"id": fmt.Sprintf("%d", reqCount2), "text": "tweet"},
			},
			"cursor": nextCursor,
		})
	}))
	defer srv2.Close()

	client2 := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv2.URL), fxtwitter.WithHTTPClient(srv2.Client()))
	tweets2, cur2, err := client2.FetchUserTimeline(ctx, "user", 100, "", 5, false, false)
	if err != nil {
		t.Fatalf("FetchUserTimeline failed: %v", err)
	}
	if reqCount2 != 3 {
		t.Errorf("reqCount2 = %d, want loop to stop after cyclic cursor (3 requests, not 5)", reqCount2)
	}
	if len(tweets2) != 3 {
		t.Errorf("got %d tweets, want 3 (one per fetched page)", len(tweets2))
	}
	// Companion assertion: see the media cycle test — the discriminating ones
	// are reqCount2 and len(tweets2) above.
	if cur2 != "" {
		t.Errorf("cursor = %q, want empty: a stalled chain has no continuation", cur2)
	}

	// 3. Verify unbounded pagination (maxPages < 0) is not clamped by default 5 pages
	reqCount3 := 0
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount3++
		w.Header().Set("Content-Type", "application/json")
		next := fmt.Sprintf("cur_%d", reqCount3)
		if reqCount3 >= 7 {
			next = ""
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"tweets": []map[string]any{
				{"id": fmt.Sprintf("%d", reqCount3), "text": "tweet"},
			},
			"cursor": next,
		})
	}))
	defer srv3.Close()

	client3 := fxtwitter.NewClient(fxtwitter.WithBaseURL(srv3.URL), fxtwitter.WithHTTPClient(srv3.Client()))
	tweets3, _, err := client3.FetchUserTimeline(ctx, "user", 100, "", -1, false, false)
	if err != nil {
		t.Fatalf("FetchUserTimeline unbounded failed: %v", err)
	}
	if reqCount3 != 7 || len(tweets3) != 7 {
		t.Errorf("unbounded: reqCount = %d, tweets = %d, want 7 pages to completion", reqCount3, len(tweets3))
	}
}
