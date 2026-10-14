package turbo

import (
	"strings"
	"testing"
)

// FuzzExtractEmbeddedItems pins that hostile album HTML can never panic the
// embedded-file extractor or produce an item without a page URL.
func FuzzExtractEmbeddedItems(f *testing.F) {
	for _, s := range []string{
		`<script>const FILES = [{ id:"a", name:"x.mp4", size: 1 }];</script>`,
		`const FILES = [{ id:"", name:"", size: 0 }];`,
		`{ id:"a", name:"a\", size: 1 }`,
		`<html></html>`,
		`const FILES = [{ id:"a", name:"\u2014.mp4", size: 1 },`,
	} {
		f.Add([]byte(s))
	}
	scraper := newTestScraper()
	f.Fuzz(func(t *testing.T, html []byte) {
		items := scraper.extractEmbeddedItems(html)
		for _, item := range items {
			if !strings.HasPrefix(item.URL, scraper.baseURL+"/d/") {
				t.Fatalf("item URL = %q, want a page URL under %s", item.URL, scraper.baseURL)
			}
		}
	})
}
