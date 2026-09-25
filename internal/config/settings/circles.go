package settings

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/pelletier/go-toml/v2"
)

// Circle represents a named collection of creator handles.
type Circle struct {
	Key         string   `toml:"key,omitempty" json:"key"`
	Name        string   `toml:"name" json:"name"`
	Description string   `toml:"description,omitempty" json:"description"`
	Users       []string `toml:"users" json:"users"`
	ListID      string   `toml:"list_id,omitempty" json:"list_id,omitempty"`
}

// CirclesConfig is the wrapper schema for ~/.nitter-cli/circles.toml.
type CirclesConfig struct {
	Circles map[string]Circle `toml:"circles"`
}

var circlesMu sync.Mutex

// LoadCircles reads circles from path. If the file does not exist, an empty
// map is returned without error. Both [circles.<key>] and top-level [<key>]
// formats are accepted.
func LoadCircles(path string) (map[string]Circle, error) {
	circlesMu.Lock()
	defer circlesMu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return make(map[string]Circle), nil
		}
		return nil, fmt.Errorf("read circles: %w", err)
	}

	// 1. Try wrapped schema: [circles.<key>]
	var cfg CirclesConfig
	if err := toml.Unmarshal(data, &cfg); err == nil && len(cfg.Circles) > 0 {
		for k, c := range cfg.Circles {
			if c.Key == "" {
				c.Key = k
			}
			if c.Name == "" {
				c.Name = k
			}
			if c.Users == nil {
				c.Users = []string{}
			}
			cfg.Circles[k] = c
		}
		return cfg.Circles, nil
	}

	// 2. Try top-level map: [<key>]
	var raw map[string]Circle
	if err := toml.Unmarshal(data, &raw); err == nil && len(raw) > 0 {
		for k, c := range raw {
			if c.Key == "" {
				c.Key = k
			}
			if c.Name == "" {
				c.Name = k
			}
			if c.Users == nil {
				c.Users = []string{}
			}
			raw[k] = c
		}
		return raw, nil
	}

	return make(map[string]Circle), nil
}

// SaveCircles atomically writes circles to path using [circles.<key>] layout.
func SaveCircles(path string, circles map[string]Circle) error {
	circlesMu.Lock()
	defer circlesMu.Unlock()

	cfg := CirclesConfig{Circles: circles}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode circles: %w", err)
	}

	return writeFileAtomic(path, data)
}

// FindCircle looks up a circle by key or name (case-insensitive fallback).
func FindCircle(circles map[string]Circle, name string) (Circle, bool) {
	_, c, ok := FindCircleKey(circles, name)
	return c, ok
}

// FindCircleKey is FindCircle with the matched map key. A circle's struct Key
// can diverge from the section key it is stored under — the inner key field
// wins over the section name on load — so write-backs must target the
// matched map key, never c.Key.
func FindCircleKey(circles map[string]Circle, name string) (string, Circle, bool) {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return "", Circle{}, false
	}
	// 1. Exact key match
	if c, ok := circles[clean]; ok {
		return clean, c, true
	}
	// 2. Exact Name match
	for k, c := range circles {
		if c.Name == clean {
			return k, c, true
		}
	}
	// 3. Case-insensitive key match
	lower := strings.ToLower(clean)
	for k, c := range circles {
		if strings.ToLower(k) == lower {
			return k, c, true
		}
	}
	// 4. Case-insensitive Name match
	for k, c := range circles {
		if strings.ToLower(c.Name) == lower {
			return k, c, true
		}
	}
	return "", Circle{}, false
}
