package cyberdrop
// internal/parser/cyberdrop/cyberdrop_test.go
package cyberdrop

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/scraper/internal/model"
)

func TestParseCyberDropURL(t *testing.T) {
	html := `<html><body>
		<a class="image-link" href="https://cdn.example.com/photo1.jpg">photo1.jpg</a>
		<a class="image-link" href="https://cdn.example.com/clip1.mp4">clip1.mp4</a>
		<a class="image-link" href="">ignored</a>
	</body></html>`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(html))
	}))
	defer ts.Close()

	scraper := NewCyberDropScraper()
	items, err := scraper.Parse(ts.URL + "/a/abc123")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	if items[0].Title != "photo1.jpg" || items[0].Type != model.MediaTypeImage {
		t.Errorf("unexpected first item: %+v", items[0])
	}
	if items[1].Title != "clip1.mp4" || items[1].Type != model.MediaTypeVideo {
		t.Errorf("unexpected second item: %+v", items[1])
	}
}

func TestParseCyberDropURLErrorStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	scraper := NewCyberDropScraper()
	if _, err := scraper.Parse(ts.URL); err == nil {
		t.Error("expected error for non-200 response")
	}
}
