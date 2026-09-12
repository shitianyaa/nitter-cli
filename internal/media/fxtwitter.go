package media

// FxTwitter backend: https://api.fxtwitter.com/<user|i>/status/<id> returns a
// JSON envelope whose tweet object (or, as a fallback, the payload top level)
// carries media.all[] — photo/video/gif entries, the video ones with a
// variants list (or a Twitter-API-shaped video_info.variants block) ranked by
// bitrate. Parsing is a faithful port of the plugin's _media_from_fxtwitter.

import (
	"encoding/json"
	"strings"

	"github.com/shitianyaa/twitter-cli/sdk"
)

const opFx = "media.fx"

// fxStatusURL is the fx endpoint URL; user-less refs use the /i/ segment
// (plugin: link.username or "i"). The base yields to the EndpointOverrides
// test seam when set.
func fxStatusURL(ref StatusRef) string {
	return baseURL(EndpointOverrides.Fx, "https://api.fxtwitter.com") + "/" + userSegment(ref) + "/status/" + ref.ID
}

// fxVariant is one entry of a variants list. fxtwitter's own variants name
// the MIME type "type"; Twitter-API-shaped video_info variants use
// "content_type" — both are accepted, with content_type winning when both
// appear.
type fxVariant struct {
	URL         string `json:"url"`
	Bitrate     int64  `json:"bitrate"`
	ContentType string `json:"content_type"`
	Type        string `json:"type"`
}

func (v fxVariant) mediaVariant() twitter.MediaVariant {
	contentType := v.ContentType
	if contentType == "" {
		contentType = v.Type
	}
	return twitter.MediaVariant{URL: v.URL, Bitrate: v.Bitrate, ContentType: contentType}
}

// fxVideoInfo mirrors the Twitter-API video_info block.
type fxVideoInfo struct {
	Variants []json.RawMessage `json:"variants"`
}

// fxMediaItem is one entry of media.all[]; a missing type defaults to photo
// (plugin semantics), so Kind is derived per item, not here.
type fxMediaItem struct {
	Type         string            `json:"type"`
	URL          string            `json:"url"`
	ThumbnailURL string            `json:"thumbnail_url"`
	Width        int               `json:"width"`
	Height       int               `json:"height"`
	Variants     []json.RawMessage `json:"variants"`
	VideoInfo    *fxVideoInfo      `json:"video_info"`
}

// fxMediaBlock is the media container; All is kept raw because the plugin
// skips non-object entries instead of failing the payload.
type fxMediaBlock struct {
	All []json.RawMessage `json:"all"`
}

// fxTweet is the tweet object's media-bearing shape.
type fxTweet struct {
	Media *fxMediaBlock `json:"media"`
}

// parseFx extracts media candidates from an api.fxtwitter.com payload:
// the tweet object's media.all[] when a tweet object is present, otherwise —
// only when a truthy text field marks the top level as status-shaped — the
// top-level media.all[] (plugin tw = data fallback). A payload that decodes
// but yields no media block returns zero candidates ("empty", not an error).
func parseFx(body []byte) ([]mediaCandidate, error) {
	var payload struct {
		Tweet json.RawMessage `json:"tweet"`
		Text  json.RawMessage `json:"text"`
		Media *fxMediaBlock   `json:"media"`
	}
	if err := decodeJSONObject(opFx, body, &payload); err != nil {
		return nil, err
	}
	block := payload.Media
	if raw := payload.Tweet; len(raw) > 0 && string(raw) != "null" {
		var tweet fxTweet
		if json.Unmarshal(raw, &tweet) != nil {
			// A tweet object outside the known shape: the plugin carries on
			// with its defaults and finds no media, so this is empty rather
			// than malformed.
			return nil, nil
		}
		block = tweet.Media
	} else if !jsonTruthy(payload.Text) {
		return nil, nil
	}
	if block == nil {
		return nil, nil
	}
	return fxCandidates(block.All), nil
}

// fxCandidates walks media.all[]: photos (and anything untyped) become image
// candidates; video/gif entries resolve their variant list — the item's own
// list, or video_info.variants when that list is empty (Python `or`
// semantics) — and keep the item URL as the variant-less direct link.
func fxCandidates(items []json.RawMessage) []mediaCandidate {
	cands := make([]mediaCandidate, 0, len(items))
	for _, item := range unmarshalItems[fxMediaItem](items) {
		kind := kindFromType(item.Type)
		url := item.URL
		if url == "" {
			url = item.ThumbnailURL
		}
		if kind == kindVideo || kind == kindGif {
			variants := item.Variants
			if len(variants) == 0 && item.VideoInfo != nil {
				variants = item.VideoInfo.Variants
			}
			cands = append(cands, mediaCandidate{
				Kind:     kind,
				URL:      url,
				Variants: fxVariants(variants),
				Width:    item.Width,
				Height:   item.Height,
			})
			continue
		}
		cands = append(cands, mediaCandidate{Kind: kind, URL: url, Width: item.Width, Height: item.Height})
	}
	return cands
}

// fxVariants decodes one variants list, skipping entries without a URL
// (plugin: empty vurl entries are passed over, not fatal).
func fxVariants(items []json.RawMessage) []twitter.MediaVariant {
	decoded := unmarshalItems[fxVariant](items)
	out := make([]twitter.MediaVariant, 0, len(decoded))
	for _, v := range decoded {
		m := v.mediaVariant()
		if m.URL == "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// jsonTruthy approximates Python truthiness for the fx top-level text gate:
// absent, null, empty string and empty object are falsy; everything else is
// truthy. Real payloads only ever exercise the string and object shapes.
func jsonTruthy(raw json.RawMessage) bool {
	switch strings.TrimSpace(string(raw)) {
	case "", "null", `""`, "{}":
		return false
	}
	return true
}
