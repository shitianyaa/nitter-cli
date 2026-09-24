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
		return nil, sdk.Errorf(sdk.KindInvalidArg, op, "create request: %w", err)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "nitter-cli/fxtwitter")

	resp, err := c.getHTTPClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, sdk.Errorf(sdk.KindUnavailable, op, "request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, sdk.Errorf(sdk.KindNotFound, op, "resource not found (404): %s", reqURL)
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
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &probe); err == nil && probe.Code != 0 {
		if probe.Code == 404 {
			return nil, sdk.Errorf(sdk.KindNotFound, op, "fxtwitter returned code 404: %s", probe.Message)
		}
		if probe.Code >= 400 {
			return nil, sdk.Errorf(sdk.KindUnavailable, op, "fxtwitter error %d: %s", probe.Code, probe.Message)
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
	if cleanUser == "" || count <= 0 {
		return nil, "", nil
	}

	var endpoint string
	if skipPlainText && filterReposts {
		endpoint = fmt.Sprintf("/2/profile/%s/media", cleanUser)
	} else {
		endpoint = fmt.Sprintf("/2/profile/%s/statuses", cleanUser)
	}

	perPage := count
	if skipPlainText {
		perPage = count * 2
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
	if pagesLimit <= 0 {
		pagesLimit = 3
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
	pagesFetched := 0
	var lastCursor string
	cursorStalled := false

	for len(accumulated) < count && pagesFetched < pagesLimit {
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
		if lastCursor == "" || lastCursor == currentCursor {
			cursorStalled = true
		}

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
	if cleanUser == "" || count <= 0 {
		return nil, "", nil
	}

	endpoint := fmt.Sprintf("/2/profile/%s/media", cleanUser)
	pagesLimit := maxPages
	if pagesLimit <= 0 {
		pagesLimit = 3
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
	pagesFetched := 0
	var lastCursor string
	cursorStalled := false

	for len(accumulated) < count && pagesFetched < pagesLimit {
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
		if lastCursor == "" || lastCursor == currentCursor {
			cursorStalled = true
		}

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

	endpoint := fmt.Sprintf("/2/profile/%s/following", cleanUser)
	params := url.Values{}
	if limit > 0 {
		params.Set("limit", strconv.Itoa(limit))
		params.Set("count", strconv.Itoa(limit))
	}
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

	if limit > 0 && len(profiles) > limit {
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
		return nil, "", nil
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

	perPage := count
	if perPage < 1 {
		perPage = 10
	}
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

	if count > 0 && len(tweets) > count {
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

// FetchQuotes retrieves quote tweets for a given status ID.
func (c *Client) FetchQuotes(ctx context.Context, statusID string, count int, cursor string) ([]sdk.Tweet, string, error) {
	cleanID := strings.TrimSpace(statusID)
	if cleanID == "" {
		return nil, "", sdk.Errorf(sdk.KindInvalidArg, opQuotes, "statusID cannot be empty")
	}

	endpoint := fmt.Sprintf("/2/status/%s/quotes", cleanID)
	params := url.Values{}
	if count > 0 {
		params.Set("count", strconv.Itoa(count))
		params.Set("limit", strconv.Itoa(count))
	}
	if cursor != "" {
		params.Set("cursor", cursor)
	}

	body, err := c.doGet(ctx, opQuotes, endpoint, params)
	if err != nil {
		var sdkErr *sdk.Error
		if errors.As(err, &sdkErr) && sdkErr.Kind == sdk.KindNotFound {
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

	if count > 0 && len(tweets) > count {
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

	perPage := count
	if perPage < 1 {
		perPage = 10
	}
	if perPage > 100 {
		perPage = 100
	}

	params := url.Values{}
	params.Set("q", q)
	params.Set("count", strconv.Itoa(perPage))
	params.Set("limit", strconv.Itoa(perPage))

	body, err := c.doGet(ctx, opSearchUsers, "/2/search/users", params)
	if err != nil {
		var sdkErr *sdk.Error
		if errors.As(err, &sdkErr) && sdkErr.Kind == sdk.KindNotFound {
			return nil, nil
		}
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

	if count > 0 && len(profiles) > count {
		profiles = profiles[:count]
	}

	return profiles, nil
}
