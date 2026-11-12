package youtube

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"golang.org/x/time/rate"
)

// googlevideo reliably serves ranges up to 2 MiB (the kkdai client's own
// limit); chunks are fetched in parallel but written in order, so the file
// on disk is always a valid prefix of the stream.
var (
	streamChunkSize    int64 = 2 << 20
	streamChunkWorkers       = 10
)

const resumeSuffix = ".resume.json"

// streamResume identifies the stream a partial work file holds. Stream URLs
// expire after a few hours, but these fields do not, so a partial file is
// resumed only when a freshly fetched format matches all of them.
type streamResume struct {
	VideoID       string `json:"videoID"`
	Itag          int    `json:"itag"`
	ContentLength int64  `json:"contentLength"`
	LastModified  string `json:"lastModified"`
}

// streamWorkDir returns the work dir for one output, creating it if needed.
// Its name is derived from the output, so a retry or a later run (after a
// crash, cancel, or exhausted retries) finds the partial streams an earlier
// attempt left. It lives beside the output, not in the system temp dir,
// which may be a small tmpfs or a different filesystem than a multi-GB
// download needs.
func streamWorkDir(dest, finalPath, identity string) (string, error) {
	dir := workDirPath(dest, finalPath, identity)
	if err := os.Mkdir(dir, 0750); err != nil && !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("youtube: work dir %s is not a directory", dir)
	}
	return dir, nil
}

// workDirPath is the stable work dir location for one output and identity.
func workDirPath(dest, finalPath, identity string) string {
	key := fileutil.SourceIdentity(identity + "\n" + finalPath)[:16]
	return filepath.Join(dest, ".scraper-yt-"+key)
}

// releaseWorkDir removes dir unless err is transient (network failure,
// cancel), in which case the partial streams are kept for a resume.
func releaseWorkDir(dir string, err error) {
	var permanent interface{ NonRetryable() }
	if err != nil && !errors.As(err, &permanent) {
		return
	}
	_ = os.RemoveAll(dir)
}

func (s *Scraper) downloadStreamToFileProgress(ctx context.Context, video *ytdl.Video, format *ytdl.Format, path string, limiter *rate.Limiter, onProgress func(int64, int64)) (string, error) {
	s.videoMu.Lock()
	streamURL, err := s.video.GetStreamURLContext(ctx, video, format)
	s.videoMu.Unlock()
	if err != nil {
		return "", err
	}

	size := format.ContentLength
	meta := streamResume{VideoID: video.ID, Itag: format.ItagNo, ContentLength: size, LastModified: format.LastModified}
	offset := resumeOffset(path, meta)
	if offset == 0 {
		if err := startStreamFile(path, meta); err != nil {
			return "", model.NewNonRetryable(err)
		}
	}

	var stream io.ReadCloser
	if size > 0 {
		stream = s.openChunkedStream(ctx, streamURL, offset, size)
	} else {
		stream, err = s.openWholeStream(ctx, streamURL)
		if err != nil {
			return "", err
		}
	}
	defer stream.Close()
	return copyStreamToFileFrom(ctx, stream, offset, size, path, limiter, onProgress)
}

// resumeOffset returns how many bytes of path can be kept, or 0 when the
// partial file is missing or belongs to a different stream.
func resumeOffset(path string, want streamResume) int64 {
	if want.ContentLength <= 0 {
		return 0
	}
	f, err := fileutil.OpenFile(path+resumeSuffix, os.O_RDONLY, 0)
	if err != nil {
		return 0
	}
	var got streamResume
	err = json.NewDecoder(io.LimitReader(f, 4096)).Decode(&got)
	_ = f.Close()
	if err != nil || got != want {
		return 0
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > want.ContentLength {
		return 0
	}
	return info.Size()
}

// startStreamFile discards any old partial data before recording meta, so
// a crash in between can never pair stale bytes with the new stream's
// metadata. Streams of unknown length can't be resumed and get no metadata.
func startStreamFile(path string, meta streamResume) error {
	for _, p := range []string{path, path + resumeSuffix} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if meta.ContentLength <= 0 {
		return nil
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	f, err := fileutil.OpenFile(path+resumeSuffix, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func setStreamHeaders(req *http.Request) {
	// googlevideo URLs are signed for the client that requested them.
	req.Header.Set("User-Agent", ytdl.DefaultClient.UserAgent)
	req.Header.Set("Origin", "https://youtube.com")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
}

func streamStatusErr(resp *http.Response) error {
	code := resp.StatusCode
	if code == http.StatusOK || code == http.StatusPartialContent {
		return nil
	}
	if model.IsRetryableStatus(code) {
		return model.NewHTTPStatusError(fmt.Errorf("youtube: stream returned HTTP %d", code), code, "", 0)
	}
	// Other statuses (403 for an expired or rejected URL) stay retryable:
	// each retry fetches fresh stream URLs.
	return &streamStatusError{Code: code}
}

// streamStatusError is YouTube refusing a stream outright (403, 400, ...).
type streamStatusError struct{ Code int }

func (e *streamStatusError) Error() string {
	return fmt.Sprintf("youtube: stream returned HTTP %d", e.Code)
}

// openWholeStream fetches a stream of unknown length in one request.
func (s *Scraper) openWholeStream(ctx context.Context, streamURL string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, model.NewNonRetryable(err)
	}
	setStreamHeaders(req)
	resp, err := s.video.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	if err := streamStatusErr(resp); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp.Body, nil
}

// openChunkedStream returns bytes offset..size-1 of the stream, fetched in
// parallel range requests. Closing it aborts the outstanding requests.
func (s *Scraper) openChunkedStream(ctx context.Context, streamURL string, offset, size int64) io.ReadCloser {
	ctx, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	go func() {
		defer cancel()
		pw.CloseWithError(s.pumpChunks(ctx, streamURL, offset, size, pw))
	}()
	return &cancelReadCloser{PipeReader: pr, cancel: cancel}
}

type cancelReadCloser struct {
	*io.PipeReader
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	c.cancel()
	return c.PipeReader.Close()
}

type chunkResult struct {
	data []byte
	err  error
}

// pumpChunks writes the chunks to w in order. The pending queue's capacity
// bounds how many chunks are fetched or buffered ahead of the writer.
func (s *Scraper) pumpChunks(ctx context.Context, streamURL string, offset, size int64, w io.Writer) error {
	// Snapshot the tunables: the producer can outlive this call by a moment.
	chunkSize := streamChunkSize
	pending := make(chan chan chunkResult, streamChunkWorkers)
	go func() {
		defer close(pending)
		for start := offset; start < size; start += chunkSize {
			end := min(start+chunkSize, size) - 1
			res := make(chan chunkResult, 1)
			select {
			case pending <- res:
			case <-ctx.Done():
				return
			}
			go func() {
				data, err := s.fetchChunk(ctx, streamURL, start, end)
				res <- chunkResult{data, err}
			}()
		}
	}()
	for res := range pending {
		r := <-res
		if r.err != nil {
			return r.err
		}
		if _, err := w.Write(r.data); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (s *Scraper) fetchChunk(ctx context.Context, streamURL string, start, end int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		return nil, model.NewNonRetryable(err)
	}
	setStreamHeaders(req)
	// A Range header, not a range query parameter: rewriting the signed URL
	// can invalidate it.
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	resp, err := s.video.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := streamStatusErr(resp); err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK && start != 0 {
		return nil, fmt.Errorf("youtube: stream ignored range request at offset %d", start)
	}
	want := end - start + 1
	data, err := io.ReadAll(io.LimitReader(resp.Body, want+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != want {
		return nil, fmt.Errorf("youtube: chunk at offset %d has %d bytes, want %d", start, len(data), want)
	}
	return data, nil
}

func hashFilePrefix(path string, n int64, h hash.Hash) error {
	f, err := fileutil.OpenFile(path, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.CopyN(h, f, n)
	return err
}
