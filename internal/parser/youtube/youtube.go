// internal/parser/youtube/youtube.go
package youtube

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/netutil"
)

type Scraper struct {
	video *ytdl.Client
	// videoMu serializes calls into video: kkdai's player-config cache is
	// unsynchronized, and concurrent downloads share this client.
	videoMu sync.Mutex

	// parsed holds video info already fetched by Parse, keyed by video ID,
	// so the download path reuses it instead of fetching the same metadata
	// a second time. Guarded by mu because downloads run concurrently.
	mu     sync.Mutex
	parsed map[string]*ytdl.Video

	// warn receives non-fatal notices (quality fallback); nil drops them.
	warn      func(string)
	maxHeight int
	audioOnly string
}

// NewYouTubeScraper creates a YouTube scraper using the kkdai/youtube library
// for real stream URL extraction and downloads.
func NewYouTubeScraper() *Scraper {
	s, _ := NewYouTubeScraperWithOptions("", nil)
	return s
}

// NewYouTubeScraperWithOptions creates a YouTube scraper whose metadata
// requests go through proxyURL (when set) and carry cookies.
func NewYouTubeScraperWithOptions(proxyURL string, cookies []*http.Cookie) (*Scraper, error) {
	client, err := netutil.NewScraperClient(proxyURL, cookies)
	if err != nil {
		return nil, err
	}
	return &Scraper{
		video:  &ytdl.Client{HTTPClient: client},
		parsed: make(map[string]*ytdl.Video),
	}, nil
}

func (s *Scraper) Name() string {
	return "youtube"
}

// SetDownloadOptions keeps scrape-time size estimates aligned with the
// formats selected by the downloader.
func (s *Scraper) SetDownloadOptions(quality, audioOnly string) {
	s.maxHeight, _ = ValidQuality(quality)
	s.audioOnly = audioOnly
}

func (s *Scraper) getVideo(ctx context.Context, videoID string) (*ytdl.Video, error) {
	s.videoMu.Lock()
	defer s.videoMu.Unlock()
	return s.video.GetVideoContext(ctx, videoID)
}

// Parse returns one MediaItem per video: a single item for a plain video
// URL, or one item per video in the playlist if the URL has a "list" query
// parameter. Quality is resolved lazily at download time (see
// selectBestFormats), not here, so parsing a large playlist doesn't have
// to pre-resolve the formats of every video up front.
func (s *Scraper) Parse(ctx context.Context, rawURL string) ([]model.MediaItem, error) {
	if ExtractPlaylistID(rawURL) != "" {
		return s.parsePlaylist(ctx, rawURL)
	}

	videoID := ExtractVideoID(rawURL)
	if videoID == "" {
		return nil, fmt.Errorf("youtube: could not extract video ID from URL")
	}

	video, err := s.getVideo(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("youtube: failed to fetch video info: %w", err)
	}

	// Hand the info to the download path (see takeParsedVideo) so the same
	// video's metadata isn't fetched again when it is downloaded.
	s.mu.Lock()
	s.parsed[videoID] = video
	s.mu.Unlock()

	title := video.Title
	if title == "" {
		title = videoID
	}

	return []model.MediaItem{{
		URL:   videoWatchURL(videoID),
		Title: title,
		Size:  estimatedDownloadSize(video, s.maxHeight, s.audioOnly != ""),
		Type:  model.MediaTypeVideo,
	}}, nil
}

// parsePlaylist returns one MediaItem per video in the playlist. Playlist
// entries only carry ID/title/author metadata (per GetPlaylistContext's own
// docs), not format info, so each item's Size is left at 0 (unknown) here;
// it's known once the video is actually resolved at download time.
func (s *Scraper) parsePlaylist(ctx context.Context, rawURL string) ([]model.MediaItem, error) {
	s.videoMu.Lock()
	playlist, err := s.video.GetPlaylistContext(ctx, rawURL)
	s.videoMu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("youtube: failed to fetch playlist info: %w", err)
	}

	items := make([]model.MediaItem, 0, len(playlist.Videos))
	for _, entry := range playlist.Videos {
		if entry == nil || entry.ID == "" {
			continue
		}
		title := entry.Title
		if title == "" {
			title = videoWatchURL(entry.ID)
		}
		items = append(items, model.MediaItem{
			URL:   videoWatchURL(entry.ID),
			Title: title,
			Type:  model.MediaTypeVideo,
		})
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("youtube: no downloadable videos found in playlist")
	}
	return items, nil
}

// ExtractVideoID pulls the video ID out of a YouTube URL, or "" if rawURL
// is not a single-video URL. It handles the modern URL shapes (/watch?v=,
// youtu.be, /shorts/, /embed/, /live/) by parsing the URL properly rather
// than substring-matching "v=" — the old approach both missed the
// path-based shapes and false-positived on unrelated query parameters that
// merely contain "v=" (e.g. ?iv=...).
func ExtractVideoID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	// Query-parameter form — checked for any host (including schemeless
	// input like "watch?v=ID") since "v" is a precise parameter name.
	if v := u.Query().Get("v"); v != "" {
		return v
	}

	host := strings.ToLower(u.Hostname())
	isYouTube := host == "youtu.be" ||
		host == "youtube.com" || strings.HasSuffix(host, ".youtube.com") ||
		host == "youtube-nocookie.com" || strings.HasSuffix(host, ".youtube-nocookie.com")
	if !isYouTube {
		return ""
	}

	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if host == "youtu.be" {
		return segments[0]
	}
	// /shorts/ID, /embed/ID, /live/ID, /v/ID
	if len(segments) == 2 {
		switch segments[0] {
		case "shorts", "embed", "live", "v":
			return segments[1]
		}
	}
	return ""
}

// ExtractPlaylistID returns the "list" query parameter from rawURL, or ""
// if there isn't one (a plain video URL, or an unparseable string).
func ExtractPlaylistID(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("list")
}

// videoWatchURL builds a canonical watch URL from a bare video ID, so
// MediaItem.URL is always something ExtractVideoID can round-trip.
func videoWatchURL(videoID string) string {
	return "https://www.youtube.com/watch?v=" + videoID
}

// --- Downloader ---
