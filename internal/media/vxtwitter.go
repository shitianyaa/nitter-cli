package media

// VxTwitter backend: https://api.vxtwitter.com/<user|i>/status/<id> returns a
// flat JSON payload. Primary media live in media_extended[] (typed direct
// links, no variants); when that yields nothing, the legacy mediaURLs /
// media_urls lists are used as image-only entries. Port of the plugin's
// _media_from_vxtwitter.

import (
	"encoding/json"
)

const opVx = "media.vx"

// vxStatusURL is the vx endpoint URL; user-less refs use the /i/ segment
// (plugin: link.username or "i").
func vxStatusURL(ref StatusRef) string {
	return "https://api.vxtwitter.com/" + userSegment(ref) + "/status/" + ref.ID
}

// vxMediaEntry is one media_extended[] item: a typed direct link.
type vxMediaEntry struct {
	Type         string `json:"type"`
	URL          string `json:"url"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// parseVx extracts media candidates from an api.vxtwitter.com payload:
// media_extended[] first (typed entries, a missing type defaults to image);
// when it yields nothing, the legacy mediaURLs / media_urls lists, taken as
// plain strings and projected as images. An empty mediaURLs list falls
// through to media_urls (Python `or` semantics); a null field behaves as
// absent, and non-string legacy entries are skipped, never fatal.
func parseVx(body []byte) ([]mediaCandidate, error) {
	var payload struct {
		MediaExtended   []json.RawMessage `json:"media_extended"`
		MediaURLs       []json.RawMessage `json:"mediaURLs"`
		MediaURLsLegacy []json.RawMessage `json:"media_urls"`
	}
	if err := decodeJSONObject(opVx, body, &payload); err != nil {
		return nil, err
	}
	cands := make([]mediaCandidate, 0)
	for _, item := range unmarshalItems[vxMediaEntry](payload.MediaExtended) {
		url := item.URL
		if url == "" {
			url = item.ThumbnailURL
		}
		cands = append(cands, mediaCandidate{Kind: kindFromType(item.Type), URL: url})
	}
	if len(cands) > 0 {
		return cands, nil
	}
	urls := payload.MediaURLs
	if len(urls) == 0 {
		urls = payload.MediaURLsLegacy
	}
	for _, raw := range urls {
		var u string
		if json.Unmarshal(raw, &u) == nil {
			cands = append(cands, mediaCandidate{Kind: kindImage, URL: u})
		}
	}
	return cands, nil
}
