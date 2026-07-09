// internal/downloader/downloader_test.go
package downloader

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/user/scraper/internal/model"
)

func TestDownload(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		w.Write(make([]byte, 100))
	}))
	defer ts.Close()

	dl := NewDownloader(5)
	item := model.MediaItem{URL: ts.URL}

	err := dl.Download(item, t.TempDir())
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
}