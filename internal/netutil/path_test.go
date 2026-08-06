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
	} {
		if got := PathSlug(test.rawURL); got != test.want {
			t.Errorf("PathSlug(%q) = %q, want %q", test.rawURL, got, test.want)
		}
	}
}
