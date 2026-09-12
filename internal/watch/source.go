package watch

import (
	"fmt"
	"strings"

	"github.com/shitianyaa/twitter-cli/internal/storage/seen"
)

// Source is one parsed watch source. Kind is one of KindUser, KindTag or
// KindList; Ref is the kind-specific reference, passed through VERBATIM to
// the underlying fetch and the persistent seen key (no unescaping or
// rewriting happens anywhere in the pipeline):
//
//   - user sources: Ref is the timeline handle ("user:NASA" → Timeline(NASA))
//   - tag sources:  Ref is the search query ("tag:%23AI" → Search(%23AI))
//   - list sources: Ref is the list ID ("list:12345" → ListTimeline(12345))
type Source struct {
	Kind string
	Ref  string
}

// ParseSource parses one watch source string of the form "<kind>:<ref>" —
// "user:NASA", "tag:%23AI" or "list:12345". The string is trimmed of
// surrounding whitespace and split exactly once at the first colon, so the
// ref may itself contain colons (e.g. the phrase search "tag:from:NASA").
// The kind must be one of user/tag/list and the ref must be non-empty; every
// other shape is an error (the watch command maps it to a usage error,
// exit 2). The ref passes through verbatim: whatever is written after the
// colon is both the seen key's ref part and the fetch argument.
func ParseSource(s string) (Source, error) {
	trimmed := strings.TrimSpace(s)
	kind, ref, found := strings.Cut(trimmed, ":")
	kind = strings.TrimSpace(kind)
	ref = strings.TrimSpace(ref)
	if !found {
		return Source{}, fmt.Errorf("source %q must be <kind>:<ref> (user:<handle>, tag:<query> or list:<id>)", s)
	}
	switch kind {
	case KindUser, KindTag, KindList:
	default:
		return Source{}, fmt.Errorf("source %q has unknown kind %q (want user, tag or list)", s, kind)
	}
	if ref == "" {
		return Source{}, fmt.Errorf("source %q has an empty ref", s)
	}
	return Source{Kind: kind, Ref: ref}, nil
}

// Key returns the persistent seen state key "<kind>:<ref>" (seen.SourceKey
// semantics). A parsed Source always has a non-empty kind and ref, so the
// key is never empty.
func (s Source) Key() string {
	return seen.SourceKey(s.Kind, s.Ref)
}
