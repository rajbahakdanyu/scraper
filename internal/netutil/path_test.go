package netutil

import "testing"

func TestPathSlug(t *testing.T) {
	for _, test := range []struct {
		rawURL string
		want   string
	}{
		{rawURL: "https://example.test/album/item/?ref=share", want: "item"},
		{rawURL: "https://example.test/item", want: "item"},
		{rawURL: "https://example.test/", want: "example.test"},
		{rawURL: "https://pixeldrain.com/l/CSbjLYKs#", want: "CSbjLYKs"},
		{rawURL: "https://cyberdrop.cr/a/abc#item-2", want: "abc"},
		{rawURL: "https://pixeldrain.com/l/CSbjLYKs/?test=1", want: "CSbjLYKs"},
		{rawURL: "https://turbo.cr/a/g2UEE4mQilU?ref=1#top", want: "g2UEE4mQilU"},
	} {
		if got := PathSlug(test.rawURL); got != test.want {
			t.Errorf("PathSlug(%q) = %q, want %q", test.rawURL, got, test.want)
		}
	}
}
