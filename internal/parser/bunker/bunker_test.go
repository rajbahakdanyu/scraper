// internal/parser/bunker/bunker_test.go
package bunker

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/scraper/internal/model"
)

func TestParseBunkerAlbum(t *testing.T) {
	// Mock the file page
	fileHTML := `<html><head><title>Jamie Lo (39).mp4 | Bunkr</title></head><body>
		<a href="https://dl.bunkr.cr/file/61567794">Download</a>
	</body></html>`

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
	mux.HandleFunc("/f/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(fileHTML))
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()

	scraper := NewBunkerScraper()
	scraper.baseURL = ts.URL
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
	if items[0].URL != "https://dl.bunkr.cr/file/61567794" {
		t.Errorf("expected dl.bunkr.cr URL, got %s", items[0].URL)
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
