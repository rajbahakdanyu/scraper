// internal/parser/bunker/bunker_test.go
package bunker

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/scraper/internal/model"
)

func TestParseBunkerURL(t *testing.T) {
	html := `<html><body>
		<a class="link" href="https://cdn.example.com/image1.png">image1.png</a>
		<a class="link" href="https://cdn.example.com/video1.mkv">video1.mkv</a>
		<a class="link" href="">ignored</a>
	</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(html))
	}))
	defer ts.Close()

	scraper := NewBunkerScraper()
	items, err := scraper.Parse(ts.URL + "/a/abc123")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].Title != "image1.png" || items[0].Type != model.MediaTypeImage {
		t.Errorf("unexpected first item: %+v", items[0])
	}
	if items[1].Title != "video1.mkv" || items[1].Type != model.MediaTypeVideo {
		t.Errorf("unexpected second item: %+v", items[1])
	}
}

func TestParseBunkerURLErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := NewBunkerScraper()
	if _, err := scraper.Parse(ts.URL); err == nil {
		t.Error("expected error for non-200 response")
	}
}
