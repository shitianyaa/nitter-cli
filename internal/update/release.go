// Release lookup for the update check: one anonymous GET against the GitHub
// Releases API, filtered and compared client-side with the package's semver
// (semver.go). Plain net/http on purpose — see the package comment.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// defaultAPIBase is the GitHub REST API root (no trailing slash usage below
// — the URL is assembled with path joins).
const defaultAPIBase = "https://api.github.com"

// requestTimeout bounds the single GET of an update check. No retries: a
// failed check reports failure (exit 1) and the user can re-run.
const requestTimeout = 15 * time.Second

// perPage asks for the maximum page size. The MVP contract assumes the
// FIRST PAGE of releases (sorted newest-first by the API) is enough: the
// client filters invalid tags and drafts itself, so up to 100 candidate
// releases are considered. A repository with more than 100 candidate
// releases published after the newest page-1 valid stable release would be
// missed — an acceptable, documented assumption for a young project.
const perPage = 100

// ErrNoRelease reports that the API answered but no usable release matched
// (no releases at all, or every candidate is a draft / invalid tag).
var ErrNoRelease = errors.New("no usable release found (drafts, prereleases and invalid tags are filtered)")

// repoShape matches "owner/repo" with URL-path-safe characters only. It
// keeps caller-supplied repo values out of the request path (no traversal,
// no query injection).
var repoShape = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Release is the selected latest release.
type Release struct {
	// Tag is the raw git tag ("v0.2.0" convention).
	Tag string
	// Version is the tag without the leading v (the form compared against
	// buildinfo.Version).
	Version string
	// Name is the release display name ("" when the release has none).
	Name string
	// URL is the release's html_url — the human download page.
	URL string
	// Prerelease reports whether the selected release is a prerelease
	// (only reachable with Options.IncludePrerelease).
	Prerelease bool
	// PublishedAt is the release publish timestamp (zero when the API
	// carries none).
	PublishedAt time.Time
	// Assets are the release's downloadable files. The installer fetches only
	// the one matching the current platform; the rest are ignored.
	Assets []Asset
}

// Asset is one downloadable file attached to a release.
type Asset struct {
	// Name is the asset's file name as published (e.g. "checksums.txt").
	Name string
	// URL is the browser_download_url — the official GitHub download path.
	URL string
}

// Options tweaks Check. The zero value checks the real GitHub API for
// stable releases only.
type Options struct {
	// BaseURL overrides the API root (tests point it at httptest).
	BaseURL string
	// IncludePrerelease admits prereleases into the "latest" selection.
	IncludePrerelease bool
	// HTTPClient overrides the default client (15s timeout).
	HTTPClient *http.Client
}

// releaseRecord is the subset of the GitHub release object this package
// consumes.
type releaseRecord struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	HTMLURL     string        `json:"html_url"`
	Draft       bool          `json:"draft"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []assetRecord `json:"assets"`
}

// assetRecord is the subset of the GitHub asset object this package uses.
type assetRecord struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// Check queries the GitHub Releases API for repo and returns the highest
// usable release by semver precedence. Drafts are always excluded; prereleases
// only with Options.IncludePrerelease; tags that do not parse as semver are
// skipped. The current version is accepted per the package's public
// signature and deliberately unused: Check reports the latest release
// regardless, so a dev build can skip the whole call — the caller computes
// outdatedness itself (Compare(release.Tag, current) > 0).
//
// Errors: a malformed repo shape, transport failures, non-200 responses
// (status code only — the body is never echoed, per the repo's redaction
// contract) and ErrNoRelease when nothing usable remains after filtering.
func Check(ctx context.Context, repo, current string, opts Options) (Release, error) {
	if !repoShape.MatchString(repo) {
		return Release{}, fmt.Errorf("update: repo %q is not in owner/repo form", repo)
	}
	_ = current // deliberately unused; see doc comment

	base := opts.BaseURL
	if base == "" {
		base = defaultAPIBase
	}
	endpoint := strings.TrimSuffix(base, "/") + "/repos/" + repo + "/releases?per_page=" + fmt.Sprint(perPage)

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return Release{}, fmt.Errorf("update: build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "nitter-cli-update-check")

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("update: github api: %w", redactURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("update: github api: HTTP %d", resp.StatusCode)
	}

	var records []releaseRecord
	// The response bound must accommodate 100 releases *with their asset
	// arrays*: parsing assets multiplies the payload (github.com/cli/cli
	// returns ~4.7 MB for one page), and a bound that is too tight truncates
	// mid-JSON into a confusing "unexpected EOF". 32 MiB stays bounded — the
	// point is to refuse an endless stream, not to cap realistic pages.
	const maxReleasePageBytes = 32 << 20
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxReleasePageBytes)).Decode(&records); err != nil {
		return Release{}, fmt.Errorf("update: github api: decode response: %w", err)
	}

	var candidates []Release
	for _, r := range records {
		if r.Draft {
			continue
		}
		if r.Prerelease && !opts.IncludePrerelease {
			continue
		}
		if !IsValid(r.TagName) {
			continue
		}
		assets := make([]Asset, 0, len(r.Assets))
		for _, a := range r.Assets {
			assets = append(assets, Asset{Name: a.Name, URL: a.BrowserDownloadURL})
		}
		candidates = append(candidates, Release{
			Tag:         r.TagName,
			Version:     strings.TrimPrefix(r.TagName, "v"),
			Name:        r.Name,
			URL:         r.HTMLURL,
			Prerelease:  r.Prerelease,
			PublishedAt: r.PublishedAt,
			Assets:      assets,
		})
	}
	if len(candidates) == 0 {
		return Release{}, ErrNoRelease
	}
	// Highest semver wins, not the API's recency order (a re-tagged or
	// back-published release must not shadow a higher version).
	sort.Slice(candidates, func(i, j int) bool {
		return Compare(candidates[i].Tag, candidates[j].Tag) > 0
	})
	return candidates[0], nil
}

// redactURLError strips net/url error wrappers, whose text embeds the full
// request URL (repo's redaction contract: URLs do not belong in errors).
func redactURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err
	}
	return err
}
