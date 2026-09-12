package result

import (
	"strconv"
	"strings"

	"github.com/shitianyaa/twitter-cli/sdk"
)

// MediaLine renders one media resolution as the human-readable,
// tab-separated row:
//
//	ref <TAB> source <TAB> kind <TAB> url <TAB> label <TAB> duration <TAB> size
//
// The ref echoes the input reference the resolution belongs to, source is
// the winning strategy, and the optional trailing cells use "-" for values
// the resolution does not carry (label empty, no probed/source duration, no
// probed size). Duration renders as a plain seconds number ("3.2"), size as
// a plain byte count — both stay script-parseable.
func MediaLine(res twitter.MediaResolution) string {
	label := res.Label
	if label == "" {
		label = "-"
	}
	duration := "-"
	if res.DurationSeconds > 0 {
		duration = strconv.FormatFloat(res.DurationSeconds, 'f', -1, 64)
	}
	size := "-"
	if res.SizeBytes > 0 {
		size = strconv.FormatInt(res.SizeBytes, 10)
	}
	cells := []string{
		res.Ref,
		res.Source,
		res.Kind,
		res.URL,
		label,
		duration,
		size,
	}
	return strings.Join(cells, "\t")
}
