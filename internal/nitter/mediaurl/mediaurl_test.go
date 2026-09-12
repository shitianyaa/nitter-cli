package mediaurl_test

import (
	"testing"

	"github.com/shitianyaa/twitter-cli/internal/nitter/mediaurl"
)

func TestRewritePBSOrig(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"replaces existing name", "https://pbs.twimg.com/media/Abc.jpg?name=large", "https://pbs.twimg.com/media/Abc.jpg?name=orig"},
		{"replaces name after other params", "https://pbs.twimg.com/media/Abc.jpg?format=jpg&name=small", "https://pbs.twimg.com/media/Abc.jpg?format=jpg&name=orig"},
		{"appends when absent", "https://pbs.twimg.com/media/Abc.jpg", "https://pbs.twimg.com/media/Abc.jpg?name=orig"},
		{"appends with ampersand", "https://pbs.twimg.com/media/Abc.jpg?format=jpg", "https://pbs.twimg.com/media/Abc.jpg?format=jpg&name=orig"},
		{"non-pbs untouched", "https://nitter.example/pic/media%2FAbc.jpg", "https://nitter.example/pic/media%2FAbc.jpg"},
		{"non-media pbs untouched", "https://pbs.twimg.com/profile_images/Abc_normal.jpg", "https://pbs.twimg.com/profile_images/Abc_normal.jpg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mediaurl.RewritePBSOrig(tc.in); got != tc.want {
				t.Errorf("RewritePBSOrig(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRewritePBSTier pins the quality-tier port of the plugin's
// prefer_pbs_quality: high=orig, medium=large, low=small, unknown → orig.
// The high tier must stay exactly RewritePBSOrig.
func TestRewritePBSTier(t *testing.T) {
	cases := []struct {
		name string
		in   string
		tier string
		want string
	}{
		{"high replaces existing name", "https://pbs.twimg.com/media/Abc.jpg?name=large", "high", "https://pbs.twimg.com/media/Abc.jpg?name=orig"},
		{"medium tier", "https://pbs.twimg.com/media/Abc.jpg?name=orig", "medium", "https://pbs.twimg.com/media/Abc.jpg?name=large"},
		{"low tier", "https://pbs.twimg.com/media/Abc.jpg?format=jpg", "low", "https://pbs.twimg.com/media/Abc.jpg?format=jpg&name=small"},
		{"medium appends when absent", "https://pbs.twimg.com/media/Abc.jpg", "medium", "https://pbs.twimg.com/media/Abc.jpg?name=large"},
		{"unknown tier falls back to orig", "https://pbs.twimg.com/media/Abc.jpg", "bogus", "https://pbs.twimg.com/media/Abc.jpg?name=orig"},
		{"empty tier falls back to orig", "https://pbs.twimg.com/media/Abc.jpg?format=jpg&name=small", "", "https://pbs.twimg.com/media/Abc.jpg?format=jpg&name=orig"},
		{"tier is case-insensitive", "https://pbs.twimg.com/media/Abc.jpg", "Medium", "https://pbs.twimg.com/media/Abc.jpg?name=large"},
		{"non-pbs untouched", "https://video.twimg.com/x.mp4", "low", "https://video.twimg.com/x.mp4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mediaurl.RewritePBSTier(tc.in, tc.tier); got != tc.want {
				t.Errorf("RewritePBSTier(%q, %q) = %q, want %q", tc.in, tc.tier, got, tc.want)
			}
		})
	}
	// The high tier is RewritePBSOrig by another name: any divergence would
	// change the frozen Nitter projection.
	if got, want := mediaurl.RewritePBSTier("https://pbs.twimg.com/media/Abc.jpg?format=jpg&name=small", "HIGH"), mediaurl.RewritePBSOrig("https://pbs.twimg.com/media/Abc.jpg?format=jpg&name=small"); got != want {
		t.Errorf("RewritePBSTier(high) = %q, RewritePBSOrig = %q", got, want)
	}
}

func TestAbsolutize(t *testing.T) {
	cases := []struct {
		name string
		base string
		in   string
		want string
	}{
		{"joins instance-relative path", "https://nitter.example", "/pic/media%2FAbc.jpg", "https://nitter.example/pic/media%2FAbc.jpg"},
		{"joins plain relative path", "https://nitter.example", "pic/media/abc.jpg", "https://nitter.example/pic/media/abc.jpg"},
		{"protocol-relative gets https", "https://nitter.example", "//pbs.twimg.com/media/abc.jpg", "https://pbs.twimg.com/media/abc.jpg"},
		{"absolute http passes through", "https://nitter.example", "http://video.twimg.com/x.mp4", "http://video.twimg.com/x.mp4"},
		{"absolute https passes through", "https://nitter.example", "https://pbs.twimg.com/media/abc.jpg", "https://pbs.twimg.com/media/abc.jpg"},
		{"foreign scheme passes through for the safety gate", "https://nitter.example", "javascript:alert(1)", "javascript:alert(1)"},
		{"no base leaves relative value", "", "/pic/media/abc.jpg", "/pic/media/abc.jpg"},
		{"empty value stays empty", "https://nitter.example", "  ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mediaurl.Absolutize(tc.base, tc.in); got != tc.want {
				t.Errorf("Absolutize(%q, %q) = %q, want %q", tc.base, tc.in, got, tc.want)
			}
		})
	}
}

// TestNormalizePicProxy pins the R13 ruling: an instance /pic/ proxy whose
// decoded target is a pbs.twimg.com media path canonicalizes to the direct
// pbs URL (name=orig), so the same photo arriving as a media:content pbs URL
// and as a percent-encoded description img proxy dedups to one entry. Other
// /pic/ targets keep their absolutized proxy form.
func TestNormalizePicProxy(t *testing.T) {
	cases := []struct {
		name          string
		base, raw     string
		want          string
		wantPBSDirect bool
	}{
		{
			name:          "percent-encoded proxy maps to pbs orig",
			base:          "https://nitter.example",
			raw:           "/pic/media%2FFxxx1.jpg%3Fname%3Dsmall",
			want:          "https://pbs.twimg.com/media/Fxxx1.jpg?name=orig",
			wantPBSDirect: true,
		},
		{
			name:          "plain proxy with query maps to pbs orig",
			base:          "https://nitter.example",
			raw:           "/pic/media/Fxxx.jpg?name=small",
			want:          "https://pbs.twimg.com/media/Fxxx.jpg?name=orig",
			wantPBSDirect: true,
		},
		{
			name:          "plain proxy without query gets name=orig",
			base:          "https://nitter.example",
			raw:           "/pic/media/Fxxx.jpg",
			want:          "https://pbs.twimg.com/media/Fxxx.jpg?name=orig",
			wantPBSDirect: true,
		},
		{
			name:          "absolute proxy URL ignores the base",
			base:          "https://other.example",
			raw:           "https://nitter.example/pic/media%2FFxxx.jpg%3Fname%3Dsmall",
			want:          "https://pbs.twimg.com/media/Fxxx.jpg?name=orig",
			wantPBSDirect: true,
		},
		{
			name:          "proxy without a base still maps to pbs",
			base:          "",
			raw:           "/pic/media%2FFxxx.jpg",
			want:          "https://pbs.twimg.com/media/Fxxx.jpg?name=orig",
			wantPBSDirect: true,
		},
		{
			name:          "existing orig name is preserved",
			base:          "https://nitter.example",
			raw:           "/pic/media%2FFxxx.jpg%3Fname%3Dorig",
			want:          "https://pbs.twimg.com/media/Fxxx.jpg?name=orig",
			wantPBSDirect: true,
		},
		{
			name:          "direct pbs URL rewrites in place",
			base:          "https://nitter.example",
			raw:           "https://pbs.twimg.com/media/Fxxx.jpg?name=small",
			want:          "https://pbs.twimg.com/media/Fxxx.jpg?name=orig",
			wantPBSDirect: true,
		},
		{
			name:          "direct pbs URL without name appends orig",
			base:          "https://nitter.example",
			raw:           "https://pbs.twimg.com/media/Fxxx.jpg",
			want:          "https://pbs.twimg.com/media/Fxxx.jpg?name=orig",
			wantPBSDirect: true,
		},
		{
			name:          "video thumb proxy keeps the absolutized form",
			base:          "https://nitter.example",
			raw:           "/pic/ext_tw_video_thumb%2F2081%2Fpu%2Fimg%2Fabc.jpg",
			want:          "https://nitter.example/pic/ext_tw_video_thumb%2F2081%2Fpu%2Fimg%2Fabc.jpg",
			wantPBSDirect: false,
		},
		{
			name:          "plain video thumb proxy keeps the absolutized form",
			base:          "https://nitter.example",
			raw:           "/pic/ext_tw_video_thumb/2081/pu/img/abc.jpg",
			want:          "https://nitter.example/pic/ext_tw_video_thumb/2081/pu/img/abc.jpg",
			wantPBSDirect: false,
		},
		{
			name:          "card image proxy is not a media path",
			base:          "https://nitter.example",
			raw:           "/pic/card_img%2Fabc.jpg",
			want:          "https://nitter.example/pic/card_img%2Fabc.jpg",
			wantPBSDirect: false,
		},
		{
			name:          "non-pic relative path just absolutizes",
			base:          "https://nitter.example",
			raw:           "/media/Fxxx.jpg",
			want:          "https://nitter.example/media/Fxxx.jpg",
			wantPBSDirect: false,
		},
		{
			name:          "broken percent escape keeps the absolutized form",
			base:          "https://nitter.example",
			raw:           "/pic/media%ZZ.jpg",
			want:          "https://nitter.example/pic/media%ZZ.jpg",
			wantPBSDirect: false,
		},
		{
			name:          "empty input stays empty",
			base:          "https://nitter.example",
			raw:           "   ",
			want:          "",
			wantPBSDirect: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, pbsDirect := mediaurl.NormalizePicProxy(tc.base, tc.raw)
			if got != tc.want {
				t.Errorf("NormalizePicProxy(%q, %q) = %q, want %q", tc.base, tc.raw, got, tc.want)
			}
			if pbsDirect != tc.wantPBSDirect {
				t.Errorf("NormalizePicProxy(%q, %q) pbsDirect = %v, want %v", tc.base, tc.raw, pbsDirect, tc.wantPBSDirect)
			}
		})
	}
}
