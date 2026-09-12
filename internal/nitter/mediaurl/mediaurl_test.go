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
