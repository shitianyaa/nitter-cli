// Package result renders human-readable command results. Per the repo
// boundary it never encodes JSON (commands own machine-readable output via
// jsonx), never builds commands and never calls the SDK — it only imports sdk
// types to project their values into text.
package result

import (
	"strconv"
	"strings"
	"time"

	"github.com/shitianyaa/nitter-cli/sdk"
)

// InstanceReportHeader is the tab-separated header line for
// InstanceReportLine output; the column names match the --json keys.
func InstanceReportHeader() string {
	return "url\trss\tuser_html\tsearch\tlist\tlatency"
}

// InstanceReportLine renders one instance report as the human-readable,
// tab-separated diagnostics line:
//
//	URL<TAB>rss<TAB>user_html<TAB>search<TAB>list<TAB>latency
//
// Probe cells: "ok" on success, "-" for a probe that did not run, and
// "fail(...)" otherwise — the HTTP status when the failure is the status
// itself (empty Err, e.g. fail(404)), the short redacted reason when the
// probe failed on content or transport ("not rss", "timeout", …). Latency is
// rendered at human precision (see formatLatency).
func InstanceReportLine(r nitter.InstanceReport) string {
	cells := []string{
		r.URL,
		formatProbe(r.RSS),
		formatProbe(r.UserHTML),
		formatProbe(r.Search),
		formatProbe(r.List),
		formatLatency(r.Latency),
	}
	return strings.Join(cells, "\t")
}

// formatProbe renders one probe cell per InstanceReportLine's contract.
func formatProbe(p nitter.Probe) string {
	switch {
	case p.OK:
		return "ok"
	case p.Err == "" && p.Status == 0:
		return "-" // not probed
	case p.Err == "":
		return "fail(" + strconv.Itoa(p.Status) + ")"
	default:
		return "fail(" + p.Err + ")"
	}
}

// formatLatency renders a duration at table precision: microseconds below a
// millisecond, milliseconds below a second, deciseconds above — never the
// raw nanosecond dump of time.Duration.String.
func formatLatency(d time.Duration) string {
	switch {
	case d <= 0:
		return "0s"
	case d < time.Millisecond:
		return d.Round(time.Microsecond).String()
	case d < time.Second:
		return d.Round(time.Millisecond).String()
	default:
		return d.Round(10 * time.Millisecond).String()
	}
}
