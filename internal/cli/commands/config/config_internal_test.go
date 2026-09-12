package config

import "testing"

// TestKnownKeysHaveGetters pins the invariant that the ordered knownKeys
// slice and the getters map stay in sync in both directions: a key listed
// without a getter would nil-panic in `config get`, and a getter without a
// knownKeys entry would be invisible in `config get` without a key.
func TestKnownKeysHaveGetters(t *testing.T) {
	for _, key := range knownKeys {
		if getters[key] == nil {
			t.Errorf("knownKeys entry %q has no getter", key)
		}
	}
	if len(getters) != len(knownKeys) {
		t.Errorf("getters has %d entries, knownKeys lists %d; the two must stay in sync", len(getters), len(knownKeys))
	}
}
