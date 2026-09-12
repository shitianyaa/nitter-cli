package result_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shitianyaa/twitter-cli/internal/cli/result"
	"github.com/shitianyaa/twitter-cli/sdk"
)

func TestInstanceReportHeader(t *testing.T) {
	if got, want := result.InstanceReportHeader(), "url\trss\tuser_html\tsearch\tlist\tlatency"; got != want {
		t.Fatalf("header = %q, want %q", got, want)
	}
}

func TestInstanceReportLine(t *testing.T) {
	t.Run("all ok", func(t *testing.T) {
		r := twitter.InstanceReport{
			URL:      "http://a",
			RSS:      twitter.Probe{OK: true, Status: 200},
			UserHTML: twitter.Probe{OK: true, Status: 200},
			Search:   twitter.Probe{OK: true, Status: 200},
			List:     twitter.Probe{OK: true, Status: 200},
			Latency:  42123456 * time.Nanosecond,
		}
		want := "http://a\tok\tok\tok\tok\t42ms"
		if got := result.InstanceReportLine(r); got != want {
			t.Fatalf("line = %q, want %q", got, want)
		}
	})
	t.Run("failures and skipped probes", func(t *testing.T) {
		r := twitter.InstanceReport{
			URL:      "http://b",
			RSS:      twitter.Probe{Status: 404},                 // HTTP-status failure: status is the reason
			UserHTML: twitter.Probe{Err: "timeout"},              // transport failure
			Search:   twitter.Probe{Status: 200, Err: "not rss"}, // content failure on a 200
			List:     twitter.Probe{},                            // not probed
			Latency:  4200 * time.Microsecond,
		}
		want := "http://b\tfail(404)\tfail(timeout)\tfail(not rss)\t-\t4ms"
		if got := result.InstanceReportLine(r); got != want {
			t.Fatalf("line = %q, want %q", got, want)
		}
	})
}

func TestInstanceReportLineLatencyFormat(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{400 * time.Microsecond, "400µs"},
		{42123456 * time.Nanosecond, "42ms"},
		{1234500000 * time.Nanosecond, "1.23s"},
	} {
		r := twitter.InstanceReport{URL: "u", Latency: tc.d}
		cells := strings.Split(result.InstanceReportLine(r), "\t")
		if got := cells[len(cells)-1]; got != tc.want {
			t.Errorf("latency %v rendered %q, want %q", tc.d, got, tc.want)
		}
	}
}
