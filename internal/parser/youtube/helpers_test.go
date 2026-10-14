package youtube

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// Fake YouTube server: tests run Parse and Download against a fake
// youtube.com so the number of video-info fetches can be counted. Every
// Client.GetVideoContext call shows up here as exactly one POST to
// /youtubei/v1/player (plus a watch-page GET for the publish date), so
// counting player posts counts metadata fetches.

type fakeYouTube struct {
	server           *httptest.Server
	playerPosts      atomic.Int32
	playerResponses  map[string]string
	streamContents   map[string]string
	streamContent    string
	playlistResponse string
	// blockStream makes /stream hang until the client goes away, for
	// in-flight cancellation tests; streamRequests counts /stream hits.
	blockStream    bool
	streamRequests atomic.Int32
	streamStarted  chan struct{}
	streamOnce     sync.Once
}

// startFakeYouTube serves every endpoint the ytdl client touches: the front
// page (visitor-ID bootstrap before any POST), the innertube player API, the
// watch page (embedding the same player response JSON), the embed page and
// base.js player config fetched while resolving the stream URL, and the
// media stream itself (honoring the client's ?range=start-end chunk
// parameter).
func startFakeYouTube(t *testing.T, videoID, title, content string) *fakeYouTube {
	t.Helper()
	f := &fakeYouTube{
		playerResponses: make(map[string]string),
		streamContents:  make(map[string]string),
		streamContent:   content,
		streamStarted:   make(chan struct{}),
	}

	var playerResponse, watchPage string
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			fmt.Fprint(w, "\nytcfg.set("+`{"INNERTUBE_CONTEXT":{"client":{"visitorData":"fakevisitor"}}}`+");")
		case r.Method == http.MethodPost && r.URL.Path == "/youtubei/v1/player":
			f.playerPosts.Add(1)
			w.Header().Set("Content-Type", "application/json")
			body, _ := io.ReadAll(r.Body)
			for id, response := range f.playerResponses {
				if strings.Contains(string(body), id) {
					fmt.Fprint(w, response)
					return
				}
			}
			fmt.Fprint(w, playerResponse)
		case r.Method == http.MethodPost && r.URL.Path == "/youtubei/v1/browse":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, f.playlistResponse)
		case r.Method == http.MethodGet && r.URL.Path == "/playlist":
			fmt.Fprint(w, f.playlistResponse)
		case r.URL.Path == "/watch":
			fmt.Fprint(w, watchPage)
		case strings.HasPrefix(r.URL.Path, "/embed/"):
			fmt.Fprint(w, `<html>"jsUrl":"/s/player/fake1234/player_ias.vflset/en_US/base.js"</html>`)
		case strings.HasPrefix(r.URL.Path, "/s/player/"):
			fmt.Fprint(w, "// fake base.js; the fake stream URL has no n-parameter to decode")
		case strings.HasPrefix(r.URL.Path, "/stream"):
			f.streamRequests.Add(1)
			f.streamOnce.Do(func() { close(f.streamStarted) })
			if f.blockStream {
				<-r.Context().Done() // hang until the client aborts
				return
			}
			if rangeParam := r.URL.Query().Get("range"); rangeParam != "" {
				var start, end int
				streamContent := f.streamContents[r.URL.Path]
				if streamContent == "" {
					streamContent = f.streamContent
				}
				if _, err := fmt.Sscanf(rangeParam, "%d-%d", &start, &end); err != nil || end >= len(streamContent) {
					http.Error(w, "bad range", http.StatusBadRequest)
					return
				}
				fmt.Fprint(w, streamContent[start:end+1])
				return
			}
			streamContent := f.streamContents[r.URL.Path]
			if streamContent == "" {
				streamContent = f.streamContent
			}
			fmt.Fprint(w, streamContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.server.Close)

	// contentLength is a string in the real player response JSON.
	playerResponse = fmt.Sprintf(`{"playabilityStatus":{"status":"OK"},`+
		`"videoDetails":{"videoId":%q,"title":%q,"lengthSeconds":"5","author":"tester"},`+
		`"streamingData":{"formats":[{"itag":18,"url":%q,"mimeType":"video/mp4","bitrate":1000,`+
		`"width":640,"height":360,"contentLength":"%d","audioChannels":2}]}}`,
		videoID, title, f.server.URL+"/stream", len(content))
	f.playerResponses[videoID] = playerResponse
	watchPage = `<html><script>var ytInitialPlayerResponse = ` + playerResponse + `;</script></html>`
	return f
}

// wire routes all of scraper's HTTP through the fake. It must be called
// after NewDownloaderWithConfig, which installs its own streaming client on
// the scraper.
func (f *fakeYouTube) wire(scraper *Scraper) {
	f.playerPosts.Store(0)
	scraper.video.HTTPClient = &http.Client{Transport: fakeYouTubeTransport{host: f.server.Listener.Addr().String()}}
}

// fakeYouTubeTransport rewrites every request to hit the fake server,
// whatever host the ytdl client originally asked for.
type fakeYouTubeTransport struct{ host string }

func (t fakeYouTubeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	redirected := req.Clone(req.Context())
	redirected.URL.Scheme = "http"
	redirected.URL.Host = t.host
	return http.DefaultTransport.RoundTrip(redirected)
}

func assertDownloadedFile(t *testing.T, dir, name, want string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("downloaded %s = %q, want %q", name, got, want)
	}
}

// waitProgressClosed drains ch until it closes, failing the test if that
// takes longer than timeout.
func waitProgressClosed(t *testing.T, ch <-chan model.ProgressEvent, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("progress channel was not closed")
		}
	}
}

func playlistJSON(entries ...string) string {
	return fmt.Sprintf(`{"metadata":{"playlistMetadataRenderer":{"title":{"runs":[{"text":"Test Playlist"}]}}},`+
		`"contents":{"twoColumnBrowseResultsRenderer":{"tabs":[{"tabRenderer":{"content":{"sectionListRenderer":{"contents":[`+
		`{"itemSectionRenderer":{"contents":[{"playlistVideoListRenderer":{"contents":[%s]}}]}}`+
		`]}}}}]}}}`, strings.Join(entries, ","))
}

func playlistEntryJSON(id, title string) string {
	titleJSON := `"runs":[]`
	if title != "" {
		titleJSON = fmt.Sprintf(`"runs":[{"text":%q}]`, title)
	}
	return fmt.Sprintf(`{"playlistVideoRenderer":{"videoId":%q,"title":{%s},"lengthSeconds":"5"}}`, id, titleJSON)
}
