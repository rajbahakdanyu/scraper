// internal/parser/bunker/bunker_test.go
package bunker

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

func TestParseBunkerAlbum(t *testing.T) {
	mux := http.NewServeMux()

	// Album page
	albumHTML := `<html><body>
<a href="/f/LJiSwHaGgRq2i" aria-label="download" target="_blank"></a>
<a href="/f/tQqpEH85Y8tjT" aria-label="download" target="_blank"></a>
</body></html>`

	mux.HandleFunc("/a/test123", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(albumHTML))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	// File page links to the mock dl server (needs ts.URL, so set after ts created)
	mux.HandleFunc("/f/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><head><title>Jamie Lo (39).mp4 | Bunkr</title></head><body>
<a href="%s/file/61567794">Download</a></body></html>`, ts.URL)
	})
	// Mock dl.bunkr.cr API
	mux.HandleFunc("/api/_001_v2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"mediafiles": "https://cdn.example.com",
			"path":       "/storage/media/test.mp4",
		})
	})
	mux.HandleFunc("/sign", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"token": "testtoken123",
			"ex":    9999999999,
		})
	})

	scraper := NewBunkerScraper()
	scraper.baseURL = ts.URL
	scraper.signURL = ts.URL + "/sign"
	items, err := scraper.Parse(ts.URL + "/a/test123")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].Title != "Jamie Lo (39).mp4" {
		t.Errorf("expected title 'Jamie Lo (39).mp4', got %s", items[0].Title)
	}
	if !strings.Contains(items[0].URL, "cdn.example.com/storage/media/test.mp4") {
		t.Errorf("expected CDN URL containing cdn.example.com, got %s", items[0].URL)
	}
	if items[0].Type != model.MediaTypeVideo {
		t.Errorf("expected video type, got %s", items[0].Type)
	}
}

func TestParseBunkerAlbumNoLinks(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><body>no files here</body></html>`))
	}))
	defer ts.Close()

	scraper := NewBunkerScraper()
	scraper.baseURL = ts.URL
	_, err := scraper.Parse(ts.URL + "/a/test")
	if err == nil {
		t.Error("expected error for album with no file links")
	}
}
