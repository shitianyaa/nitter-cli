package fxtwitter

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sdk "github.com/shitianyaa/nitter-cli/sdk"
)

// RawCursor decodes cursor from either a plain string or a cursor object
// containing "bottom", "next", or "top" fields.
type RawCursor struct {
	Value string
}

func (c *RawCursor) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		c.Value = strings.TrimSpace(s)
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err == nil {
		if bottom, ok := obj["bottom"].(string); ok && bottom != "" {
			c.Value = strings.TrimSpace(bottom)
			return nil
		}
		if next, ok := obj["next"].(string); ok && next != "" {
			c.Value = strings.TrimSpace(next)
			return nil
		}
		if top, ok := obj["top"].(string); ok && top != "" {
			c.Value = strings.TrimSpace(top)
			return nil
		}
	}
	return nil
}

// RawAuthor represents the author block of a status or a user profile.
type RawAuthor struct {
	ID                   json.Number `json:"id"`
	IDStr                string      `json:"id_str"`
	Name                 string      `json:"name"`
	ScreenName           string      `json:"screen_name"`
	Username             string      `json:"username"`
	AvatarURL            string      `json:"avatar_url"`
	ProfileImageURL      string      `json:"profile_image_url"`
	ProfileImageURLHTTPS string      `json:"profile_image_url_https"`
	BannerURL            string      `json:"banner_url"`
	ProfileBannerURL     string      `json:"profile_banner_url"`
	Description          string      `json:"description"`
	Bio                  string      `json:"bio"`
	Followers            int         `json:"followers"`
	FollowersCount       int         `json:"followers_count"`
	Following            int         `json:"following"`
	FollowingCount       int         `json:"following_count"`
	FriendsCount         int         `json:"friends_count"`
	Tweets               int         `json:"tweets"`
	TweetsCount          int         `json:"tweets_count"`
	StatusesCount        int         `json:"statuses_count"`
	MediaCount           int         `json:"media_count"`
	Protected            bool        `json:"protected"`
	IsProtected          bool        `json:"is_protected"`
}

// ToSDKProfile converts RawAuthor to sdk.Profile.
func (u *RawAuthor) ToSDKProfile() *sdk.Profile {
	if u == nil {
		return nil
	}
	id := u.IDStr
	if id == "" && u.ID.String() != "" {
		id = u.ID.String()
	}
	handle := strings.TrimPrefix(strings.TrimSpace(u.ScreenName), "@")
	if handle == "" {
		handle = strings.TrimPrefix(strings.TrimSpace(u.Username), "@")
	}
	bio := u.Bio
	if bio == "" {
		bio = u.Description
	}
	followers := u.FollowersCount
	if followers == 0 {
		followers = u.Followers
	}
	following := u.FollowingCount
	if following == 0 {
		following = u.FriendsCount
	}
	if following == 0 {
		following = u.Following
	}
	tweets := u.TweetsCount
	if tweets == 0 {
		tweets = u.StatusesCount
	}
	if tweets == 0 {
		tweets = u.Tweets
	}
	avatar := u.AvatarURL
	if avatar == "" {
		avatar = u.ProfileImageURLHTTPS
	}
	if avatar == "" {
		avatar = u.ProfileImageURL
	}
	banner := u.BannerURL
	if banner == "" {
		banner = u.ProfileBannerURL
	}
	isProtected := u.IsProtected || u.Protected

	return &sdk.Profile{
		ID:             id,
		Handle:         handle,
		Name:           u.Name,
		Bio:            bio,
		FollowersCount: followers,
		FollowingCount: following,
		TweetsCount:    tweets,
		MediaCount:     u.MediaCount,
		AvatarURL:      avatar,
		BannerURL:      banner,
		IsProtected:    isProtected,
	}
}

// RawMediaItem represents one attachment item in a tweet's media block.
type RawMediaItem struct {
	Type         string  `json:"type"`
	URL          string  `json:"url"`
	ThumbnailURL string  `json:"thumbnail_url"`
	Width        int     `json:"width"`
	Height       int     `json:"height"`
	Duration     float64 `json:"duration"`
}

// ToSDK converts RawMediaItem to sdk.Media.
func (m *RawMediaItem) ToSDK() sdk.Media {
	kind := strings.ToLower(strings.TrimSpace(m.Type))
	switch kind {
	case "photo", "image":
		kind = "image"
	case "video":
		kind = "video"
	case "gif", "animated_gif":
		kind = "gif"
	default:
		if kind == "" {
			kind = "image"
		}
	}
	mediaURL := m.URL
	if mediaURL == "" {
		mediaURL = m.ThumbnailURL
	}
	return sdk.Media{
		Type:   kind,
		URL:    mediaURL,
		Width:  m.Width,
		Height: m.Height,
	}
}

// RawMediaBlock represents the media block containing all attachments.
type RawMediaBlock struct {
	All    []RawMediaItem `json:"all"`
	Photos []RawMediaItem `json:"photos"`
	Videos []RawMediaItem `json:"videos"`
}

// RawTweet represents one tweet in raw FxTwitter API v2 format.
type RawTweet struct {
	ID                  json.Number     `json:"id"`
	IDStr               string          `json:"id_str"`
	URL                 string          `json:"url"`
	Text                string          `json:"text"`
	RawText             json.RawMessage `json:"raw_text"`
	CreatedAt           string          `json:"created_at"`
	CreatedTimestamp    int64           `json:"created_timestamp"`
	Author              *RawAuthor      `json:"author"`
	Media               *RawMediaBlock  `json:"media"`
	IsRetweet           bool            `json:"is_retweet"`
	Retweet             bool            `json:"retweet"`
	RetweetedStatus     *RawTweet       `json:"retweeted_status"`
	RepostedBy          json.RawMessage `json:"reposted_by"`
	ReplyTo             json.RawMessage `json:"reply_to"`
	ReplyingTo          json.RawMessage `json:"replying_to"`
	InReplyToScreenName string          `json:"in_reply_to_screen_name"`
	Quote               *RawTweet       `json:"quote"`
	QuotedStatus        *RawTweet       `json:"quoted_status"`
}

// RawTweetItem handles both flat RawTweet and nested {"tweet": ...}.
type RawTweetItem struct {
	RawTweet
	Tweet *RawTweet `json:"tweet"`
}

// Resolve returns the underlying RawTweet.
func (item *RawTweetItem) Resolve() *RawTweet {
	if item.Tweet != nil {
		return item.Tweet
	}
	return &item.RawTweet
}

// parseFxTime parses time from various formats used by Twitter and FxTwitter.
func parseFxTime(createdAt string, createdTimestamp int64) time.Time {
	if createdAt != "" {
		layouts := []string{
			"Mon Jan 02 15:04:05 -0700 2006", // RubyDate (Twitter classic)
			time.RFC1123,                     // "Sat, 05 Jul 2026 09:09:40 GMT"
			time.RFC1123Z,
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02 15:04:05",
		}
		for _, layout := range layouts {
			if t, err := time.Parse(layout, createdAt); err == nil {
				return t.UTC()
			}
		}
	}
	if createdTimestamp > 0 {
		if createdTimestamp > 1e11 {
			return time.UnixMilli(createdTimestamp).UTC()
		}
		return time.Unix(createdTimestamp, 0).UTC()
	}
	return time.Time{}
}

// parseReplyTo flexibly parses reply targets from either string ("user") or object ({"screen_name": "user"}).
func parseReplyTo(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var strVal string
	if err := json.Unmarshal(raw, &strVal); err == nil {
		return strVal
	}
	var objVal struct {
		ScreenName string `json:"screen_name"`
	}
	if err := json.Unmarshal(raw, &objVal); err == nil {
		return objVal.ScreenName
	}
	return ""
}

// ToSDK converts RawTweet to sdk.Tweet.
func (t *RawTweet) ToSDK() *sdk.Tweet {
	if t == nil {
		return nil
	}
	id := t.IDStr
	if id == "" && t.ID.String() != "" {
		id = t.ID.String()
	}

	var author sdk.Author
	if t.Author != nil {
		handle := strings.TrimPrefix(strings.TrimSpace(t.Author.ScreenName), "@")
		if handle == "" {
			handle = strings.TrimPrefix(strings.TrimSpace(t.Author.Username), "@")
		}
		avatar := t.Author.AvatarURL
		if avatar == "" {
			avatar = t.Author.ProfileImageURLHTTPS
		}
		if avatar == "" {
			avatar = t.Author.ProfileImageURL
		}
		author = sdk.Author{
			Handle:    handle,
			Name:      t.Author.Name,
			AvatarURL: avatar,
		}
	}

	twURL := strings.TrimSpace(t.URL)
	if twURL == "" && id != "" {
		if author.Handle != "" {
			twURL = fmt.Sprintf("https://x.com/%s/status/%s", author.Handle, id)
		} else {
			twURL = fmt.Sprintf("https://x.com/i/status/%s", id)
		}
	}

	text := t.Text
	if text == "" && len(t.RawText) > 0 {
		var s string
		if err := json.Unmarshal(t.RawText, &s); err == nil {
			text = s
		}
	}

	publishedAt := parseFxTime(t.CreatedAt, t.CreatedTimestamp)

	var media []sdk.Media
	if t.Media != nil {
		items := t.Media.All
		if len(items) == 0 {
			items = append(items, t.Media.Photos...)
			items = append(items, t.Media.Videos...)
		}
		for _, m := range items {
			media = append(media, m.ToSDK())
		}
	}

	isRetweet := t.IsRetweet || t.Retweet || t.RetweetedStatus != nil
	var repostedBy string
	if len(t.RepostedBy) > 0 && string(t.RepostedBy) != "null" {
		isRetweet = true
		var s string
		if err := json.Unmarshal(t.RepostedBy, &s); err == nil {
			repostedBy = s
		} else {
			var obj struct {
				Name       string `json:"name"`
				ScreenName string `json:"screen_name"`
			}
			if err := json.Unmarshal(t.RepostedBy, &obj); err == nil {
				repostedBy = obj.Name
				if repostedBy == "" {
					repostedBy = obj.ScreenName
				}
			}
		}
	}

	replyTo := parseReplyTo(t.ReplyTo)
	if replyTo == "" {
		replyTo = parseReplyTo(t.ReplyingTo)
	}
	if replyTo == "" {
		replyTo = t.InReplyToScreenName
	}
	replyTo = strings.TrimPrefix(strings.TrimSpace(replyTo), "@")

	var quote *sdk.Quoted
	rawQuote := t.Quote
	if rawQuote == nil {
		rawQuote = t.QuotedStatus
	}
	if rawQuote != nil {
		qSDK := rawQuote.ToSDK()
		if qSDK != nil {
			quote = &sdk.Quoted{
				ID:     qSDK.ID,
				URL:    qSDK.URL,
				Text:   qSDK.Text,
				Author: qSDK.Author,
			}
		}
	}

	return &sdk.Tweet{
		ID:          id,
		URL:         twURL,
		Text:        text,
		Author:      author,
		PublishedAt: publishedAt,
		Media:       media,
		IsRetweet:   isRetweet,
		RepostedBy:  repostedBy,
		ReplyTo:     replyTo,
		Quote:       quote,
	}
}

// RawProfileResponse represents the response from /2/profile/:id.
type RawProfileResponse struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	User    *RawAuthor `json:"user"`
	Profile *RawAuthor `json:"profile"`
	RawAuthor
}

// ToSDK converts RawProfileResponse to *sdk.Profile.
func (r *RawProfileResponse) ToSDK() *sdk.Profile {
	if r == nil {
		return nil
	}
	if r.User != nil {
		return r.User.ToSDKProfile()
	}
	if r.Profile != nil {
		return r.Profile.ToSDKProfile()
	}
	return r.RawAuthor.ToSDKProfile()
}

// RawFollowingResponse represents the response from /2/profile/:id/following.
type RawFollowingResponse struct {
	Code      int          `json:"code"`
	Message   string       `json:"message"`
	Users     []*RawAuthor `json:"users"`
	Results   []*RawAuthor `json:"results"`
	Following []*RawAuthor `json:"following"`
	Profiles  []*RawAuthor `json:"profiles"`
	Cursor    *RawCursor   `json:"cursor"`
}

// UserList returns all user profiles in the response.
func (r *RawFollowingResponse) UserList() []*RawAuthor {
	if len(r.Users) > 0 {
		return r.Users
	}
	if len(r.Results) > 0 {
		return r.Results
	}
	if len(r.Following) > 0 {
		return r.Following
	}
	return r.Profiles
}

// CursorValue returns the cursor string.
func (r *RawFollowingResponse) CursorValue() string {
	if r.Cursor != nil {
		return r.Cursor.Value
	}
	return ""
}

// RawConversationResponse represents the response from /2/conversation/:id.
type RawConversationResponse struct {
	Code    int            `json:"code"`
	Message string         `json:"message"`
	Tweet   *RawTweetItem  `json:"tweet"`
	Status  *RawTweetItem  `json:"status"`
	Thread  []RawTweetItem `json:"thread"`
	Replies []RawTweetItem `json:"replies"`
	Cursor  *RawCursor     `json:"cursor"`
}

// ToSDK converts RawConversationResponse to *sdk.Conversation.
func (r *RawConversationResponse) ToSDK() *sdk.Conversation {
	if r == nil {
		return nil
	}
	var focal sdk.Tweet
	if r.Tweet != nil {
		if t := r.Tweet.Resolve().ToSDK(); t != nil {
			focal = *t
		}
	} else if r.Status != nil {
		if t := r.Status.Resolve().ToSDK(); t != nil {
			focal = *t
		}
	}

	thread := make([]sdk.Tweet, 0, len(r.Thread))
	for _, item := range r.Thread {
		if t := item.Resolve().ToSDK(); t != nil {
			thread = append(thread, *t)
		}
	}

	replies := make([]sdk.Tweet, 0, len(r.Replies))
	for _, item := range r.Replies {
		if t := item.Resolve().ToSDK(); t != nil {
			replies = append(replies, *t)
		}
	}

	cursor := ""
	if r.Cursor != nil {
		cursor = r.Cursor.Value
	}

	return &sdk.Conversation{
		Status:  focal,
		Thread:  thread,
		Replies: replies,
		Cursor:  cursor,
	}
}

// RawStatusResponse represents the response from /2/status/:id.
type RawStatusResponse struct {
	Code    int           `json:"code"`
	Message string        `json:"message"`
	Tweet   *RawTweetItem `json:"tweet"`
	Status  *RawTweetItem `json:"status"`
	RawTweetItem
}

// ToSDK converts RawStatusResponse to *sdk.Tweet.
func (r *RawStatusResponse) ToSDK() *sdk.Tweet {
	if r == nil {
		return nil
	}
	if r.Tweet != nil {
		return r.Tweet.Resolve().ToSDK()
	}
	if r.Status != nil {
		return r.Status.Resolve().ToSDK()
	}
	return r.RawTweetItem.Resolve().ToSDK()
}

// RawTimelineResponse represents timeline and search responses.
type RawTimelineResponse struct {
	Code     int            `json:"code"`
	Message  string         `json:"message"`
	Results  []RawTweetItem `json:"results"`
	Tweets   []RawTweetItem `json:"tweets"`
	Statuses []RawTweetItem `json:"statuses"`
	Quotes   []RawTweetItem `json:"quotes"`
	Cursor   *RawCursor     `json:"cursor"`
}

// Items returns the tweets slice from whichever key is populated.
func (r *RawTimelineResponse) Items() []RawTweetItem {
	if len(r.Results) > 0 {
		return r.Results
	}
	if len(r.Tweets) > 0 {
		return r.Tweets
	}
	if len(r.Statuses) > 0 {
		return r.Statuses
	}
	return r.Quotes
}

// CursorValue returns the cursor string.
func (r *RawTimelineResponse) CursorValue() string {
	if r.Cursor != nil {
		return r.Cursor.Value
	}
	return ""
}

// RawTrend represents one trending topic from /2/trends.
type RawTrend struct {
	Name          string   `json:"name"`
	Rank          *int     `json:"rank"`
	Context       string   `json:"context"`
	DomainContext string   `json:"domain_context"`
	TweetCount    int      `json:"tweet_count"`
	TweetsCount   int      `json:"tweets_count"`
	PostCount     int      `json:"post_count"`
	Count         int      `json:"count"`
	GroupedTopics []string `json:"grouped_topics"`
	GroupedTrends []string `json:"grouped_trends"`
}

// ToSDK converts RawTrend to sdk.Trend with fallback rank.
func (t *RawTrend) ToSDK(defaultRank int) sdk.Trend {
	rank := defaultRank
	if t.Rank != nil && *t.Rank > 0 {
		rank = *t.Rank
	}
	context := t.Context
	if context == "" {
		context = t.DomainContext
	}
	tweetCount := t.TweetCount
	if tweetCount == 0 {
		tweetCount = t.TweetsCount
	}
	if tweetCount == 0 {
		tweetCount = t.PostCount
	}
	if tweetCount == 0 {
		tweetCount = t.Count
	}
	topics := t.GroupedTopics
	if len(topics) == 0 {
		topics = t.GroupedTrends
	}
	return sdk.Trend{
		Name:          t.Name,
		Rank:          rank,
		Context:       context,
		TweetCount:    tweetCount,
		GroupedTopics: topics,
	}
}

// RawTrendsResponse represents the response from /2/trends.
type RawTrendsResponse struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Trends  []RawTrend `json:"trends"`
	Results []RawTrend `json:"results"`
}

// RawUsersResponse represents the response from /2/search/users.
type RawUsersResponse struct {
	Code     int          `json:"code"`
	Message  string       `json:"message"`
	Users    []*RawAuthor `json:"users"`
	Results  []*RawAuthor `json:"results"`
	Profiles []*RawAuthor `json:"profiles"`
}

// UserList returns all user profiles in the response.
func (r *RawUsersResponse) UserList() []*RawAuthor {
	if len(r.Users) > 0 {
		return r.Users
	}
	if len(r.Results) > 0 {
		return r.Results
	}
	return r.Profiles
}
