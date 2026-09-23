package settings

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Profile is one circle member's sidecar record: machine-refreshed facts plus
// human/agent judgement. Refresh overwrites the fact fields (Name, Bio,
// FollowersCount, FetchedAt) and never touches the judgement fields (Role,
// Note, NotedAt).
type Profile struct {
	Handle         string `toml:"handle,omitempty" json:"handle"`
	Name           string `toml:"name,omitempty" json:"name"`
	Bio            string `toml:"bio,omitempty" json:"bio"`
	FollowersCount int    `toml:"followers_count,omitempty" json:"followers_count"`
	FetchedAt      string `toml:"fetched_at,omitempty" json:"fetched_at"`
	Role           string `toml:"role,omitempty" json:"role"`
	Note           string `toml:"note,omitempty" json:"note"`
	NotedAt        string `toml:"noted_at,omitempty" json:"-"`
}

// ProfilesConfig is the wrapper schema for ~/.nitter-cli/profiles.toml.
type ProfilesConfig struct {
	Profiles map[string]Profile `toml:"profiles"`
}

// LoadProfiles reads the sidecar at path. A missing file yields an empty map
// without error; a malformed file is a real error the caller must surface.
func LoadProfiles(path string) (map[string]Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]Profile), nil
		}
		return nil, fmt.Errorf("read profiles: %w", err)
	}
	var cfg ProfilesConfig
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse profiles: %w", err)
	}
	if cfg.Profiles == nil {
		return make(map[string]Profile), nil
	}
	// Normalize keys to the lookup key (lowercase) and backfill Handle from
	// the key. Without this a hand-authored `[profiles.Doubao23333]` block is
	// never found by FindProfile and a later refresh would add a second,
	// lowercase entry — stranding the hand-recorded judgement on a key nobody
	// reads. Keys are walked in sorted order so a (nonsensical) input with two
	// keys that collide case-insensitively resolves deterministically.
	keys := make([]string, 0, len(cfg.Profiles))
	for k := range cfg.Profiles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]Profile, len(cfg.Profiles))
	for _, k := range keys {
		v := cfg.Profiles[k]
		if v.Handle == "" {
			v.Handle = strings.TrimSpace(k)
		}
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out, nil
}

// SaveProfiles atomically writes the sidecar using the [profiles.<key>] layout.
func SaveProfiles(path string, profiles map[string]Profile) error {
	if profiles == nil {
		profiles = make(map[string]Profile)
	}
	data, err := toml.Marshal(ProfilesConfig{Profiles: profiles})
	if err != nil {
		return fmt.Errorf("encode profiles: %w", err)
	}
	if err := writeFileAtomic(path, data); err != nil {
		return fmt.Errorf("write profiles: %w", err)
	}
	return nil
}

// FindProfile looks up a profile by handle, case-insensitively.
func FindProfile(profiles map[string]Profile, handle string) (Profile, bool) {
	p, ok := profiles[strings.ToLower(strings.TrimSpace(handle))]
	return p, ok
}

// MergeProfileFacts returns a copy of existing with handle's fact fields
// replaced by fresh's. Judgement fields (Role, Note, NotedAt) are carried over
// verbatim; a handle with no existing entry starts with empty judgement.
// Unrelated entries are preserved untouched.
func MergeProfileFacts(existing map[string]Profile, handle string, fresh Profile, now time.Time) map[string]Profile {
	key := strings.ToLower(strings.TrimSpace(handle))
	out := make(map[string]Profile, len(existing)+1)
	for k, v := range existing {
		out[k] = v
	}
	prev, had := out[key]
	merged := Profile{
		Handle:         fresh.Handle,
		Name:           fresh.Name,
		Bio:            fresh.Bio,
		FollowersCount: fresh.FollowersCount,
		FetchedAt:      now.UTC().Format(time.RFC3339),
	}
	if merged.Handle == "" {
		merged.Handle = strings.TrimSpace(handle)
	}
	if had {
		// Judgement survives; the canonical handle stays only if the fetch
		// gave us nothing better.
		merged.Role = prev.Role
		merged.Note = prev.Note
		merged.NotedAt = prev.NotedAt
	}
	out[key] = merged
	return out
}

// ProfileAge renders a fetched_at timestamp as a coarse, single-unit age.
// An empty or unparseable timestamp means "not cached" and renders as "-".
func ProfileAge(fetchedAt string, now time.Time) string {
	if strings.TrimSpace(fetchedAt) == "" {
		return "-"
	}
	ts, err := time.Parse(time.RFC3339, fetchedAt)
	if err != nil {
		return "-"
	}
	d := now.UTC().Sub(ts.UTC())
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	case d < 14*24*time.Hour:
		return strconv.Itoa(int(d.Hours()/24)) + "d"
	default:
		return strconv.Itoa(int(d.Hours()/(24*7))) + "w"
	}
}
