package cmd

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectSite(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://youtube.com/watch?v=abc", "youtube"},
		{"https://www.youtube.com/watch?v=abc", "youtube"},
		{"https://youtu.be/abc", "youtube"},
		{"https://cyberdrop.cr/a/abc", "cyberdrop"},
		{"https://cyberdrop.me/a/abc", "cyberdrop"},
		{"https://cyberdrop.to/a/abc", "cyberdrop"},
		{"https://bunkr.cr/a/abc", "bunker"},
		{"https://bunkr.su/a/abc", "bunker"},
		{"https://bunkrr.su/a/abc", "bunker"},
		{"https://bunkr.la/a/abc", "bunker"},
		{"https://bunkr.ph/a/abc", "bunker"},
		{"https://goonbox.cr/a/abc", "goonbox"},
		{"https://goonbox.to/a/abc", "goonbox"},
		{"https://filester.sh/f/abc", "filester"},
		{"https://filester.me/d/abc", "filester"},
		{"https://filester.sh:8443/f/abc", "filester"},
	}

	for _, tt := range tests {
		got, err := detectSite(tt.url)
		if err != nil {
			t.Errorf("detectSite(%s) returned error: %v", tt.url, err)
			continue
		}
		if got != tt.want {
			t.Errorf("detectSite(%s) = %s, want %s", tt.url, got, tt.want)
		}
	}
}

func TestDetectSiteRejectsLookalikeDomains(t *testing.T) {
	// These contain a known site's name as a substring but aren't actually
	// that site, so they must not match (a plain strings.Contains check
	// on the host would incorrectly match these).
	lookalikes := []string{
		"https://notcyberdrop.example.com/a/abc",
		"https://cyberdrop.example.com/a/abc",
		"https://evil-bunkr.com/a/abc",
		"https://filester.sh.evil.com/f/abc",
	}
	for _, u := range lookalikes {
		if got, err := detectSite(u); err == nil {
			t.Errorf("detectSite(%s) = %s, want an error for a lookalike domain", u, got)
		}
	}
}

func TestDetectSiteUnknown(t *testing.T) {
	if _, err := detectSite("https://example.com/whatever"); err == nil {
		t.Error("expected error for an unrecognized domain")
	}
	if _, err := detectSite("not a url"); err == nil {
		t.Error("expected error for an unparseable URL")
	}
}

func TestScraperRegistry(t *testing.T) {
	reg, err := scraperRegistry("", nil)
	if err != nil {
		t.Fatalf("scraperRegistry failed: %v", err)
	}
	for _, name := range []string{"youtube", "cyberdrop", "bunker", "goonbox", "filester"} {
		if _, ok := reg[name]; !ok {
			t.Errorf("expected registry to contain %q", name)
		}
	}
}

func TestAlbumSlugForYouTubePlaylistUsesPlaylistID(t *testing.T) {
	tests := []struct {
		name     string
		siteName string
		url      string
		want     string
	}{
		{
			name:     "youtube playlist uses the playlist ID, not the generic slug",
			siteName: "youtube",
			url:      "https://www.youtube.com/playlist?list=PLxyz123",
			want:     "PLxyz123",
		},
		{
			name:     "youtube video with no playlist falls back to the video ID",
			siteName: "youtube",
			url:      "https://www.youtube.com/watch?v=abc123",
			want:     "abc123",
		},
		{
			name:     "non-youtube sites always use the generic slug",
			siteName: "cyberdrop",
			url:      "https://cyberdrop.cr/a/ALBUM123",
			want:     "ALBUM123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := albumSlugFor(tt.siteName, tt.url); got != tt.want {
				t.Errorf("albumSlugFor(%q, %q) = %q, want %q", tt.siteName, tt.url, got, tt.want)
			}
		})
	}

	// The bug this guards against: two different playlists must not
	// collide into the same subfolder.
	slugA := albumSlugFor("youtube", "https://www.youtube.com/playlist?list=PLaaa")
	slugB := albumSlugFor("youtube", "https://www.youtube.com/playlist?list=PLbbb")
	if slugA == slugB {
		t.Errorf("expected different playlists to get different slugs, both got %q", slugA)
	}
}

func TestBrowserExtensionDomainsMatchSiteDomains(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "browser-extension", "collect.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	for site, domains := range siteDomains {
		for _, domain := range domains {
			if !strings.Contains(string(src), `"`+domain+`"`) {
				t.Errorf("browser-extension/collect.mjs is missing %s domain %q", site, domain)
			}
		}
	}
}

func TestScraperRegistryWithCookies(t *testing.T) {
	cookies := []*http.Cookie{{Name: "auth", Value: "secret"}}
	reg, err := scraperRegistry("", cookies)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"youtube", "cyberdrop", "bunker", "goonbox", "filester"} {
		if reg[name] == nil {
			t.Errorf("expected scraper for %q in registry", name)
		}
	}
}

func TestScraperRegistryWithProxyAndCookies(t *testing.T) {
	cookies := []*http.Cookie{{Name: "auth", Value: "secret"}}
	reg, err := scraperRegistry("http://proxy:8080", cookies)
	if err != nil {
		if strings.Contains(err.Error(), "invalid proxy URL") {
			return
		}
		t.Fatalf("unexpected error: %v", err)
	}
	for _, name := range []string{"youtube", "cyberdrop", "bunker", "goonbox", "filester"} {
		if reg[name] == nil {
			t.Errorf("expected scraper for %q in registry", name)
		}
	}
}
