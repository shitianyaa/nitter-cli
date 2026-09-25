package fxtwitter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	sdk "github.com/shitianyaa/nitter-cli/sdk"
)

const (
	// DefaultBaseURL is the default FxTwitter API endpoint.
	DefaultBaseURL = "https://api.fxtwitter.com"
	// DefaultTimeout is the default HTTP client request timeout.
	DefaultTimeout = 15 * time.Second
	// maxResponseBytes limits response payload reading to 5 MB.
	maxResponseBytes = 5 * 1024 * 1024

	opUserTimeline  = "fxtwitter.FetchUserTimeline"
	opUserMedia     = "fxtwitter.FetchUserMedia"
	opUserProfile   = "fxtwitter.FetchUserProfile"
	opUserFollowing = "fxtwitter.FetchUserFollowing"
	opConversation  = "fxtwitter.FetchConversation"
	opSearch        = "fxtwitter.SearchTweets"
	opStatus        = "fxtwitter.FetchStatus"
	opQuotes        = "fxtwitter.FetchQuotes"
	opTrends        = "fxtwitter.FetchTrends"
	opSearchUsers   = "fxtwitter.SearchUsers"
)

// EndpointOverrides allows overriding default endpoints in tests.
var EndpointOverrides struct {
	BaseURL string
}

// Client is a client for FxTwitter API v2 endpoints.
type Client struct {
	BaseURL    string
	Timeout    time.Duration
	HTTPClient *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API base URL.
func WithBaseURL(rawURL string) Option {
	return func(c *Client) {
		c.BaseURL = strings.TrimRight(rawURL, "/")
	}
}

// WithTimeout sets the request timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		c.Timeout = d
	}
}

// WithHTTPClient injects a custom *http.Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		c.HTTPClient = hc
	}
}

// NewClient returns a new Client with the given options.
func NewClient(opts ...Option) *Client {
	c := &Client{
		BaseURL: DefaultBaseURL,
		Timeout: DefaultTimeout,
	}
	if EndpointOverrides.BaseURL != "" {
		c.BaseURL = strings.TrimRight(EndpointOverrides.BaseURL, "/")
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

func (c *Client) getHTTPClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	return &http.Client{Timeout: timeout}
}

func (c *Client) getBaseURL() string {
	if c.BaseURL != "" {
		return strings.TrimRight(c.BaseURL, "/")
	}
	if EndpointOverrides.BaseURL != "" {
		return strings.TrimRight(EndpointOverrides.BaseURL, "/")
	}
	return DefaultBaseURL
}

func (c *Client) doGet(ctx context.Context, op string, endpoint string, query url.Values) ([]byte, error) {
	reqURL := c.getBaseURL() + endpoint
	if len(query) > 0 {
		reqURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, sdk.Errorf(sdk.KindInvalidArg, op, "create request: %w", redactURLError(err))
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "nitter-cli/fxtwitter")

	resp, err := c.getHTTPClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, sdk.Errorf(sdk.KindUnavailable, op, "request failed: %w", redactURLError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// The query string carries the caller's own search terms, so the error
		// names the route only — never reqURL (sdk/errors.go redaction).
		return nil, sdk.Errorf(sdk.KindNotFound, op, "resource not found (404): %s", endpoint)
	}
	if resp.StatusCode >= 400 {
		return nil, sdk.Errorf(sdk.KindUnavailable, op, "upstream HTTP error %d", resp.StatusCode)
	}

	lr := io.LimitReader(resp.Body, int64(maxResponseBytes)+1)
	data, err := io.ReadAll(lr)
	if err != nil {
		return nil, sdk.Errorf(sdk.KindMalformed, op, "read response: %w", err)
	}
	if len(data) > maxResponseBytes {
		return nil, sdk.Errorf(sdk.KindMalformed, op, "response too large")
	}
	if len(data) == 0 {
		return nil, sdk.Errorf(sdk.KindMalformed, op, "empty response")
	}

	var probe struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal(data, &probe); err == nil && probe.Code != 0 {
		if probe.Code == 404 {
			return nil, sdk.Errorf(sdk.KindNotFound, op, "fxtwitter returned code 404")
		}
		if probe.Code >= 400 {
			return nil, sdk.Errorf(sdk.KindUnavailable, op, "fxtwitter error %d", probe.Code)
		}
	}

	return data, nil
}

// IsNotFound reports whether err represents a 404 / SafeSearch error.
func IsNotFound(err error) bool {
	var terr *sdk.Error
	if errors.As(err, &terr) {
		return terr.Kind == sdk.KindNotFound
	}
	return false
}

// redactURLError serves the sdk/errors.go redaction contract on the FxTwitter
// lane. A *url.Error renders the full request URL — query string included —
// into its Error() text ("Get \"https://…/2/search?q=…\": dial tcp …"), so the
// wrapper is replaced by its cause before it enters the *sdk.Error chain. Every
// transport failure would otherwise print the caller's search terms. The httpx
// lane has its own sanitizer, unexported in another package; this lane builds
// its own net/http client and does not share that transport.
func redactURLError(err error) error {
	// Repeated, not once: http.Client.Do wraps whatever the transport returned
	// in its own *url.Error, so a transport that already reports one yields a
	// nested pair and a single unwrap would leave the inner URL in place.
	// Bounded: a *url.Error whose Err points back into its own chain would
	// otherwise spin forever, and past the cap the text cannot be proven clean
	// — so the redaction stays total and says nothing instead.
	for i := 0; i < 8; i++ {
		var ue *url.Error
		if !errors.As(err, &ue) {
			return err
		}
		if ue.Err == nil {
			return errors.New("transport failure")
		}
		err = ue.Err
	}
	return errors.New("transport failure")
}

// FetchUserTimeline retrieves tweets for a given user handle with pagination and filtering.
func (c *Client) FetchUserTimeline(
	ctx context.Context,
	handle string,
	count int,
	cursor string,
	maxPages int,
	skipPlainText bool,
	filterReposts bool,
) ([]sdk.Tweet, string, error) {
	cleanUser := strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if cleanUser == "" {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opUserTimeline, "handle cannot be empty")
	}
	// count <= 0 used to return an empty success, which turned "everything"
	// into "nothing" one layer above; the CLI has no unlimited value any more
	// and resolves every limit to a positive cap (or a large sentinel) before
	// calling, so a non-positive count is a caller bug and must be loud.
	if count <= 0 {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opUserTimeline, "count must be >= 1, got %d", count)
	}

	var endpoint string
	if skipPlainText && filterReposts {
		endpoint = fmt.Sprintf("/2/profile/%s/media", cleanUser)
	} else {
		endpoint = fmt.Sprintf("/2/profile/%s/statuses", cleanUser)
	}

	perPage := count
	if skipPlainText {
		if count > 50 {
			perPage = 100
		} else {
			perPage = count * 2
		}
		if perPage < 20 {
			perPage = 20
		}
	}
	if perPage < 1 {
		perPage = 1
	}
	if perPage > 100 {
		perPage = 100
	}

	pagesLimit := maxPages
	if pagesLimit == 0 {
		pagesLimit = 5
	}

	// capacityHint clamps the eager allocation: the count doubles as the
	// accumulated slice's capacity, and an unbounded "all" count (watch's
	// math.MaxInt sentinel) must not reserve gigabytes up front — the slice
	// grows on append instead.
	capacityHint := count
	if capacityHint > 1000 {
		capacityHint = 1000
	}
	accumulated := make([]sdk.Tweet, 0, capacityHint)
	currentCursor := cursor
	seenCursors := make(map[string]bool)
	if currentCursor != "" {
		seenCursors[currentCursor] = true
	}
	pagesFetched := 0
	var lastCursor string
	cursorStalled := false

	for len(accumulated) < count && (pagesLimit < 0 || pagesFetched < pagesLimit) {
		params := url.Values{}
		params.Set("count", strconv.Itoa(perPage))
		if currentCursor != "" {
			params.Set("cursor", currentCursor)
		}

		body, err := c.doGet(ctx, opUserTimeline, endpoint, params)
		if err != nil {
			return nil, "", err
		}
		pagesFetched++

		var resp RawTimelineResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, "", sdk.Errorf(sdk.KindMalformed, opUserTimeline, "decode response: %w", err)
		}

		rawResults := resp.Items()
		if len(rawResults) == 0 {
			break
		}

		lastCursor = resp.CursorValue()
		if lastCursor == "" || seenCursors[lastCursor] {
			cursorStalled = true
		}
		seenCursors[lastCursor] = true

		for _, raw := range rawResults {
			tw := raw.Resolve().ToSDK()
			if tw == nil || (tw.ID == "" && tw.Text == "" && len(tw.Media) == 0) {
				continue
			}
			if filterReposts && tw.IsRetweet {
				continue
			}
			if skipPlainText && len(tw.Media) == 0 {
				continue
			}
			accumulated = append(accumulated, *tw)
			if len(accumulated) >= count {
				break
			}
		}

		if cursorStalled || len(accumulated) >= count {
			break
		}
		currentCursor = lastCursor
	}

	effectiveCursor := lastCursor
	if cursorStalled || lastCursor == "" {
		effectiveCursor = ""
	}
	if len(accumulated) > count {
		accumulated = accumulated[:count]
	}
	return accumulated, effectiveCursor, nil
}

// FetchUserMedia fetches media tweets for a user.
func (c *Client) FetchUserMedia(
	ctx context.Context,
	handle string,
	count int,
	cursor string,
	maxPages int,
) ([]sdk.Tweet, string, error) {
	cleanUser := strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if cleanUser == "" {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opUserMedia, "handle cannot be empty")
	}
	// See FetchUserTimeline: a non-positive count is a caller bug, not "none".
	if count <= 0 {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opUserMedia, "count must be >= 1, got %d", count)
	}

	endpoint := fmt.Sprintf("/2/profile/%s/media", cleanUser)
	pagesLimit := maxPages
	if pagesLimit == 0 {
		pagesLimit = 5
	}

	perPage := count
	if perPage < 1 {
		perPage = 1
	}
	if perPage > 100 {
		perPage = 100
	}

	// capacityHint clamps the eager allocation: the count doubles as the
	// accumulated slice's capacity, and an unbounded "all" count (watch's
	// math.MaxInt sentinel) must not reserve gigabytes up front — the slice
	// grows on append instead.
	capacityHint := count
	if capacityHint > 1000 {
		capacityHint = 1000
	}
	accumulated := make([]sdk.Tweet, 0, capacityHint)
	currentCursor := cursor
	seenCursors := make(map[string]bool)
	if currentCursor != "" {
		seenCursors[currentCursor] = true
	}
	pagesFetched := 0
	var lastCursor string
	cursorStalled := false

	for len(accumulated) < count && (pagesLimit < 0 || pagesFetched < pagesLimit) {
		params := url.Values{}
		params.Set("count", strconv.Itoa(perPage))
		if currentCursor != "" {
			params.Set("cursor", currentCursor)
		}

		body, err := c.doGet(ctx, opUserMedia, endpoint, params)
		if err != nil {
			return nil, "", err
		}
		pagesFetched++

		var resp RawTimelineResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			return nil, "", sdk.Errorf(sdk.KindMalformed, opUserMedia, "decode response: %w", err)
		}

		rawResults := resp.Items()
		if len(rawResults) == 0 {
			break
		}

		lastCursor = resp.CursorValue()
		// currentCursor is always already in seenCursors (it is seeded before
		// the loop and re-seeded from the previous page's cursor), so a
		// self-repeat is covered here without a separate comparison.
		if lastCursor == "" || seenCursors[lastCursor] {
			cursorStalled = true
		}
		seenCursors[lastCursor] = true

		for _, raw := range rawResults {
			tw := raw.Resolve().ToSDK()
			if tw == nil || (tw.ID == "" && tw.Text == "" && len(tw.Media) == 0) {
				continue
			}
			accumulated = append(accumulated, *tw)
			if len(accumulated) >= count {
				break
			}
		}

		if cursorStalled || len(accumulated) >= count {
			break
		}
		currentCursor = lastCursor
	}

	effectiveCursor := lastCursor
	if cursorStalled || lastCursor == "" {
		effectiveCursor = ""
	}
	if len(accumulated) > count {
		accumulated = accumulated[:count]
	}
	return accumulated, effectiveCursor, nil
}

// FetchUserProfile retrieves the profile of a user.
func (c *Client) FetchUserProfile(ctx context.Context, handle string) (*sdk.Profile, error) {
	cleanUser := strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if cleanUser == "" {
		return nil, sdk.Errorf(sdk.KindInvalidArg, opUserProfile, "handle cannot be empty")
	}

	endpoint := fmt.Sprintf("/2/profile/%s", cleanUser)
	body, err := c.doGet(ctx, opUserProfile, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var resp RawProfileResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, sdk.Errorf(sdk.KindMalformed, opUserProfile, "decode response: %w", err)
	}

	profile := resp.ToSDK()
	if profile == nil || (profile.ID == "" && profile.Handle == "") {
		return nil, sdk.Errorf(sdk.KindNotFound, opUserProfile, "profile not found for user: %s", cleanUser)
	}

	return profile, nil
}

// FetchUserFollowing retrieves a user's following list.
func (c *Client) FetchUserFollowing(
	ctx context.Context,
	handle string,
	limit int,
	cursor string,
) ([]sdk.Profile, string, error) {
	cleanUser := strings.TrimPrefix(strings.TrimSpace(handle), "@")
	if cleanUser == "" {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opUserFollowing, "handle cannot be empty")
	}
	// limit <= 0 used to be sent upstream as an unbounded request; the CLI
	// resolves every limit to a positive cap before calling, so a non-positive
	// limit is a caller bug and must be loud (same contract as FetchUserTimeline).
	if limit <= 0 {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opUserFollowing, "limit must be >= 1, got %d", limit)
	}

	endpoint := fmt.Sprintf("/2/profile/%s/following", cleanUser)
	params := url.Values{}
	params.Set("limit", strconv.Itoa(limit))
	params.Set("count", strconv.Itoa(limit))
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	body, err := c.doGet(ctx, opUserFollowing, endpoint, params)
	if err != nil {
		return nil, "", err
	}

	var resp RawFollowingResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", sdk.Errorf(sdk.KindMalformed, opUserFollowing, "decode response: %w", err)
	}

	users := resp.UserList()
	profiles := make([]sdk.Profile, 0, len(users))
	for _, u := range users {
		if p := u.ToSDKProfile(); p != nil {
			profiles = append(profiles, *p)
		}
	}

	nextCursor := resp.CursorValue()
	if nextCursor == cursor {
		nextCursor = ""
	}

	if len(profiles) > limit {
		profiles = profiles[:limit]
	}
	return profiles, nextCursor, nil
}

// FetchConversation retrieves the status, parent thread, and replies for a status ID.
func (c *Client) FetchConversation(
	ctx context.Context,
	statusID string,
	rankingMode string,
	cursor string,
) (*sdk.Conversation, error) {
	cleanID := strings.TrimSpace(statusID)
	if cleanID == "" {
		return nil, sdk.Errorf(sdk.KindInvalidArg, opConversation, "statusID cannot be empty")
	}

	endpoint := fmt.Sprintf("/2/conversation/%s", cleanID)
	params := url.Values{}
	if rankingMode != "" {
		params.Set("ranking_mode", rankingMode)
		params.Set("rankingMode", rankingMode)
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	body, err := c.doGet(ctx, opConversation, endpoint, params)
	if err != nil {
		return nil, err
	}

	var resp RawConversationResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, sdk.Errorf(sdk.KindMalformed, opConversation, "decode response: %w", err)
	}

	conv := resp.ToSDK()
	if conv == nil {
		return nil, sdk.Errorf(sdk.KindMalformed, opConversation, "empty conversation response")
	}

	return conv, nil
}

// SearchTweets queries FxTwitter search endpoint. feed selects the result
// ordering: "latest" (the default when empty) or "top" (popular results).
// Both values are forwarded verbatim as the endpoint's `feed` parameter;
// anything else is rejected as KindInvalidArg rather than being sent
// upstream or silently swapped for a default.
func (c *Client) SearchTweets(
	ctx context.Context,
	query string,
	count int,
	cursor string,
	feed string,
) ([]sdk.Tweet, string, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opSearch, "query cannot be empty")
	}

	feed = strings.ToLower(strings.TrimSpace(feed))
	if feed == "" {
		feed = "latest"
	}
	switch feed {
	case "latest", "top":
	default:
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opSearch, "invalid feed %q; must be latest or top", feed)
	}

	// count <= 0 used to fall back to the upstream default page size; the CLI
	// resolves every limit to a positive cap before calling, so a non-positive
	// count is a caller bug and must be loud (same contract as FetchUserTimeline).
	if count <= 0 {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opSearch, "count must be >= 1, got %d", count)
	}
	perPage := count
	if perPage > 100 {
		perPage = 100
	}

	params := url.Values{}
	params.Set("q", q)
	params.Set("feed", feed)
	params.Set("count", strconv.Itoa(perPage))
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	body, err := c.doGet(ctx, opSearch, "/2/search", params)
	if err != nil {
		return nil, "", err
	}

	var resp RawTimelineResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", sdk.Errorf(sdk.KindMalformed, opSearch, "decode response: %w", err)
	}

	items := resp.Items()
	tweets := make([]sdk.Tweet, 0, len(items))
	for _, item := range items {
		if tw := item.Resolve().ToSDK(); tw != nil {
			tweets = append(tweets, *tw)
		}
	}

	nextCursor := resp.CursorValue()
	if nextCursor == cursor {
		nextCursor = ""
	}

	if len(tweets) > count {
		tweets = tweets[:count]
	}
	return tweets, nextCursor, nil
}

// FetchStatus retrieves a single status by its ID.
func (c *Client) FetchStatus(ctx context.Context, statusID string) (*sdk.Tweet, error) {
	cleanID := strings.TrimSpace(statusID)
	if cleanID == "" {
		return nil, sdk.Errorf(sdk.KindInvalidArg, opStatus, "statusID cannot be empty")
	}

	endpoint := fmt.Sprintf("/2/status/%s", cleanID)
	body, err := c.doGet(ctx, opStatus, endpoint, nil)
	if err != nil {
		return nil, err
	}

	var resp RawStatusResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, sdk.Errorf(sdk.KindMalformed, opStatus, "decode response: %w", err)
	}

	tweet := resp.ToSDK()
	if tweet == nil || tweet.ID == "" {
		return nil, sdk.Errorf(sdk.KindNotFound, opStatus, "status not found: %s", cleanID)
	}

	return tweet, nil
}

// quoteCount decodes an integer and stays "unknown" for every other JSON shape
// (array, string, object, null, fractional number). It must never fail the
// enclosing decode: "quotes" is an ARRAY in FxTwitter's timeline payloads
// (RawTimelineResponse.Quotes), so a strict int in the shared model would turn
// a legitimate timeline response into a KindMalformed error.
type quoteCount struct {
	N     int
	Known bool
}

func (q *quoteCount) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		q.N, q.Known = n, true
	}
	return nil
}

// quotesProbe is the minimal projection of a /2/status/{id} payload used only
// by FetchQuotes' 404 cross-check. It deliberately avoids the shared Raw*
// model so the check cannot affect any other payload shape. The count is read
// from every position FxTwitter uses for a single status, because the live
// probe pinned it to the nested status object without distinguishing the
// spellings; a wrong guess fails safe (unknown keeps the 404). Pointer fields
// give "null" the same meaning for free — json leaves them nil.
type quotesProbe struct {
	Quotes *quoteCount `json:"quotes"`
	Tweet  *struct {
		Quotes *quoteCount `json:"quotes"`
	} `json:"tweet"`
	Status *struct {
		Quotes *quoteCount `json:"quotes"`
	} `json:"status"`
}

// count returns the tweet's own quote count when the payload carried it as an
// integer. ok=false means "cannot be read" — never zero.
func (p *quotesProbe) count() (n int, ok bool) {
	if p == nil {
		return 0, false
	}
	if p.Tweet != nil && p.Tweet.Quotes != nil && p.Tweet.Quotes.Known {
		return p.Tweet.Quotes.N, true
	}
	if p.Status != nil && p.Status.Quotes != nil && p.Status.Quotes.Known {
		return p.Status.Quotes.N, true
	}
	if p.Quotes != nil && p.Quotes.Known {
		return p.Quotes.N, true
	}
	return 0, false
}

// statusQuoteCount reads a tweet's own quote count from /2/status/{id} — the
// cross-check that disambiguates the 404 of /2/status/{id}/quotes, which
// FxTwitter returns both for "this tweet has no quotes" and for a pure upstream
// failure (both verified live 2026-09-24). known=false means the count could
// not be read (the status fetch failed, or the payload carried no integer
// count); callers must treat that as unknown, never as zero. The request rides
// the same transport, base URL and timeout as every other Fx call.
func (c *Client) statusQuoteCount(ctx context.Context, statusID string) (int, bool) {
	body, err := c.doGet(ctx, opStatus, fmt.Sprintf("/2/status/%s", statusID), nil)
	if err != nil {
		return 0, false
	}
	var probe quotesProbe
	if err := json.Unmarshal(body, &probe); err != nil {
		return 0, false
	}
	return probe.count()
}

// FetchQuotes retrieves quote tweets for a given status ID.
func (c *Client) FetchQuotes(ctx context.Context, statusID string, count int, cursor string) ([]sdk.Tweet, string, error) {
	cleanID := strings.TrimSpace(statusID)
	if cleanID == "" {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opQuotes, "statusID cannot be empty")
	}
	// count <= 0 used to be sent upstream without a page cap; the CLI resolves
	// every limit to a positive cap before calling, so a non-positive count is
	// a caller bug and must be loud (same contract as FetchUserTimeline).
	if count <= 0 {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opQuotes, "count must be >= 1, got %d", count)
	}

	endpoint := fmt.Sprintf("/2/status/%s/quotes", cleanID)
	params := url.Values{}
	params.Set("count", strconv.Itoa(count))
	params.Set("limit", strconv.Itoa(count))
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	body, err := c.doGet(ctx, opQuotes, endpoint, params)
	if err != nil {
		if !IsNotFound(err) {
			return nil, "", err
		}
		// This endpoint shares one upstream SearchTimeline query with
		// /2/search and /2/search/users, so it fails with the same 404 — and a
		// tweet with zero quotes answers 404 too (both verified live). Only a
		// readable count of exactly 0 makes this the legitimate empty result; a
		// positive or unreadable count keeps the failure, because a 404 must
		// never be reported as an empty success (CONTRIBUTING.md).
		if n, known := c.statusQuoteCount(ctx, cleanID); known && n == 0 {
			return nil, "", nil
		}
		return nil, "", err
	}

	var resp RawTimelineResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, "", sdk.Errorf(sdk.KindMalformed, opQuotes, "decode response: %w", err)
	}

	items := resp.Items()
	tweets := make([]sdk.Tweet, 0, len(items))
	for _, item := range items {
		if tw := item.Resolve().ToSDK(); tw != nil {
			tweets = append(tweets, *tw)
		}
	}

	nextCursor := resp.CursorValue()
	if nextCursor == cursor {
		nextCursor = ""
	}

	if len(tweets) > count {
		tweets = tweets[:count]
	}

	return tweets, nextCursor, nil
}

// FetchTrends retrieves currently trending topics.
func (c *Client) FetchTrends(ctx context.Context) ([]sdk.Trend, error) {
	body, err := c.doGet(ctx, opTrends, "/2/trends", nil)
	if err != nil {
		return nil, err
	}

	var rawList []RawTrend
	var resp RawTrendsResponse
	if err := json.Unmarshal(body, &resp); err == nil {
		rawList = resp.Trends
		if len(rawList) == 0 {
			rawList = resp.Results
		}
	} else if err := json.Unmarshal(body, &rawList); err != nil {
		return nil, sdk.Errorf(sdk.KindMalformed, opTrends, "decode response: %w", err)
	}

	trends := make([]sdk.Trend, 0, len(rawList))
	for i, raw := range rawList {
		if strings.TrimSpace(raw.Name) == "" {
			continue
		}
		trends = append(trends, raw.ToSDK(i+1))
	}

	return trends, nil
}

// SearchUsers queries FxTwitter user search endpoint.
func (c *Client) SearchUsers(ctx context.Context, query string, count int) ([]sdk.Profile, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, sdk.Errorf(sdk.KindInvalidArg, opSearchUsers, "query cannot be empty")
	}
	// count <= 0 used to fall back to the upstream default page size; the CLI
	// resolves every limit to a positive cap before calling, so a non-positive
	// count is a caller bug and must be loud (same contract as FetchUserTimeline).
	if count <= 0 {
		return nil, sdk.Errorf(sdk.KindInvalidArg, opSearchUsers, "count must be >= 1, got %d", count)
	}

	perPage := count
	if perPage > 100 {
		perPage = 100
	}

	params := url.Values{}
	params.Set("q", q)
	params.Set("count", strconv.Itoa(perPage))
	params.Set("limit", strconv.Itoa(perPage))

	body, err := c.doGet(ctx, opSearchUsers, "/2/search/users", params)
	if err != nil {
		// No 404 special case: on this endpoint a query with genuinely no
		// matches answers 200 with an empty user list (verified live
		// 2026-09-24), so a 404 is a pure failure signal and must surface.
		return nil, err
	}

	var users []*RawAuthor
	var resp RawUsersResponse
	if err := json.Unmarshal(body, &resp); err == nil {
		users = resp.UserList()
	} else if err := json.Unmarshal(body, &users); err != nil {
		return nil, sdk.Errorf(sdk.KindMalformed, opSearchUsers, "decode response: %w", err)
	}

	profiles := make([]sdk.Profile, 0, len(users))
	for _, u := range users {
		if p := u.ToSDKProfile(); p != nil {
			profiles = append(profiles, *p)
		}
	}

	if len(profiles) > count {
		profiles = profiles[:count]
	}

	return profiles, nil
}
