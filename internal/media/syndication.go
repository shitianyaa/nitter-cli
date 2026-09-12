package media

// Syndication backend: https://cdn.syndication.twimg.com/tweet-result
// ?id=<id>&token=x returns Twitter's embed payload — photos[] entries (with
// dimensions) and, for statuses with moving media, a video object whose
// variants[] list is ranked by bitrate. GIFs are detected through the
// "tweet_video_thumb" marker riding in the raw video JSON (it appears as a
// variant content type — and poster path — on GIF posts) or a gif video
// type. Port of the plugin's _media_from_syndication.

import (
	"encoding/json"
	"strings"

	"github.com/shitianyaa/nitter-cli/sdk"
)

const opSyndication = "media.syndication"

// syndicationURL is the tweet-result endpoint. token=x is the plugin's
// constant: the endpoint requires the parameter but accepts any value. The
// base yields to the EndpointOverrides test seam when set.
func syndicationURL(id string) string {
	return baseURL(EndpointOverrides.Syndication, "https://cdn.syndication.twimg.com") + "/tweet-result?id=" + id + "&token=x"
}

// syndPhoto is one photos[] entry; the URL prefers url over src.
type syndPhoto struct {
	URL    string `json:"url"`
	Src    string `json:"src"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// syndVariant is one video.variants[] entry: src is the syndication spelling,
// url the Twitter-API one; both are accepted.
type syndVariant struct {
	Src         string `json:"src"`
	URL         string `json:"url"`
	Bitrate     int64  `json:"bitrate"`
	ContentType string `json:"content_type"`
}

// syndVideo is the video object's decoded shape; the raw JSON is kept for
// the gif marker scan.
type syndVideo struct {
	Variants  []json.RawMessage `json:"variants"`
	VideoType string            `json:"video_type"`
	Type      string            `json:"type"`
}

// parseSyndication extracts media candidates from a tweet-result payload:
// photos[] become image candidates (objects with url/src, or bare URL
// strings; anything else is skipped like the plugin's str() values), and a
// video object becomes one video/gif candidate carrying its variants. A
// video whose variants yield no URL contributes nothing (plugin
// `if best:` guard), and a non-object video is skipped entirely.
func parseSyndication(body []byte) ([]mediaCandidate, error) {
	var payload struct {
		Photos []json.RawMessage `json:"photos"`
		Video  json.RawMessage   `json:"video"`
	}
	if err := decodeJSONObject(opSyndication, body, &payload); err != nil {
		return nil, err
	}
	cands := make([]mediaCandidate, 0, len(payload.Photos)+1)
	for _, raw := range payload.Photos {
		if photo, ok := decodeAs[syndPhoto](raw); ok {
			url := photo.URL
			if url == "" {
				url = photo.Src
			}
			cands = append(cands, mediaCandidate{Kind: kindImage, URL: url, Width: photo.Width, Height: photo.Height})
			continue
		}
		if u, ok := decodeAs[string](raw); ok {
			cands = append(cands, mediaCandidate{Kind: kindImage, URL: u})
		}
	}
	if len(payload.Video) == 0 || string(payload.Video) == "null" {
		return cands, nil
	}
	var video syndVideo
	if json.Unmarshal(payload.Video, &video) != nil {
		// A non-object video is skipped, not fatal (plugin isinstance guard).
		return cands, nil
	}
	variants := syndVariants(video.Variants)
	if len(variants) == 0 {
		// The plugin appends a video entry only when some variant carries a
		// URL (`if best:`); syndication offers no item URL to fall back to.
		return cands, nil
	}
	cands = append(cands, mediaCandidate{
		Kind:     syndicationKind(payload.Video, video),
		Variants: variants,
	})
	return cands, nil
}

// syndicationKind applies the gif markers: "tweet_video_thumb" anywhere in
// the raw video JSON (variant content type or poster path of a GIF post),
// or a gif-bearing video type. Everything else is a plain video.
func syndicationKind(rawVideo json.RawMessage, video syndVideo) string {
	if strings.Contains(string(rawVideo), "tweet_video_thumb") {
		return kindGif
	}
	vtype := video.VideoType
	if vtype == "" {
		vtype = video.Type
	}
	if strings.Contains(strings.ToLower(vtype), "gif") {
		return kindGif
	}
	return kindVideo
}

// syndVariants decodes one variants list (src preferred over url), skipping
// entries without a URL (plugin: empty vurl entries are passed over).
func syndVariants(items []json.RawMessage) []nitter.MediaVariant {
	decoded := unmarshalItems[syndVariant](items)
	out := make([]nitter.MediaVariant, 0, len(decoded))
	for _, v := range decoded {
		u := v.Src
		if u == "" {
			u = v.URL
		}
		if u == "" {
			continue
		}
		out = append(out, nitter.MediaVariant{URL: u, Bitrate: v.Bitrate, ContentType: v.ContentType})
	}
	return out
}
