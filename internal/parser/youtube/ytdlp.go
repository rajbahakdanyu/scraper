package youtube

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	ytdl "github.com/kkdai/youtube/v2"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

// Backends select how videos are downloaded: the built-in client, yt-dlp,
// or the built-in client with a yt-dlp fallback (auto).
const (
	BackendAuto    = "auto"
	BackendBuiltin = "builtin"
	BackendYTDLP   = "yt-dlp"
)

// ValidBackend reports whether name is a supported backend ("" = auto).
func ValidBackend(name string) bool {
	switch name {
	case "", BackendAuto, BackendBuiltin, BackendYTDLP:
		return true
	}
	return false
}

const (
	ytDLPBinary     = "yt-dlp"
	ytDLPProgress   = "SCRAPER_PROGRESS "
	ytDLPFile       = "SCRAPER_FILE:"
	ytDLPCookieFile = "cookies.txt"
)

var videoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// errYTDLPMissing is returned when the yt-dlp backend is forced but the
// binary is not on PATH.
var errYTDLPMissing = errors.New("youtube: yt-dlp not found on PATH (install it, or use --youtube-backend builtin)")

// fallbackWorthy reports whether a built-in failure is one yt-dlp can get
// past: YouTube refusing the stream (403/400), a cipher or format the
// built-in client can't handle, or video info it can't read (age or
// sign-in gates, which yt-dlp handles with cookies).
func fallbackWorthy(err error) bool {
	var status *streamStatusError
	return errors.As(err, &status) ||
		errors.Is(err, ytdl.ErrCipherNotFound) ||
		errors.Is(err, errNoFormats) ||
		errors.Is(err, errVideoInfo)
}

func (d *Downloader) ytDLPArgs(videoID string, cookieFile string) []string {
	args := []string{
		"--ignore-config", "--no-playlist", "--newline", "--progress", "--no-mtime",
		"--print", "after_move:" + ytDLPFile + "%(filepath)s",
		"--progress-template", "download:" + ytDLPProgress +
			"%(progress.downloaded_bytes)s %(progress.total_bytes)s %(progress.total_bytes_estimate)s %(progress.filename)s",
		"-o", "ytdlp.%(ext)s",
	}
	if d.audioOnly != "" {
		args = append(args, "-f", "ba/b", "-x", "--audio-format", d.audioOnly)
	} else {
		format := "bv*+ba/b"
		if d.quality > 0 {
			format = fmt.Sprintf("bv*[height<=%[1]d]+ba/b[height<=%[1]d]/%s", d.quality, format)
		}
		// Prefer H.264/AAC at the best allowed resolution: yt-dlp's default
		// AV1/Opus picks don't play in QuickTime or on many TVs.
		args = append(args, "-f", format, "-S", "res,vcodec:h264,acodec:aac", "--merge-output-format", "mp4", "--remux-video", "mp4")
	}
	if d.proxyURL != "" {
		args = append(args, "--proxy", d.proxyURL)
	}
	if d.rateLimit > 0 {
		args = append(args, "--limit-rate", strconv.FormatInt(d.rateLimit, 10))
	}
	if cookieFile != "" {
		args = append(args, "--cookies", cookieFile)
	}
	return append(args, "--", videoWatchURL(videoID))
}

// downloadWithYTDLP downloads videoID with yt-dlp into a resumable work dir
// and commits the result like the built-in paths do, so a later run skips
// it either way.
func (d *Downloader) downloadWithYTDLP(ctx context.Context, item model.MediaItem, videoID, dest string, onProgress func(int64, int64)) (finalPath string, err error) {
	if !videoIDPattern.MatchString(videoID) {
		return "", model.NewNonRetryable(fmt.Errorf("youtube: invalid video ID %q", videoID))
	}
	bin, err := exec.LookPath(ytDLPBinary)
	if err != nil {
		return "", model.NewNonRetryable(errYTDLPMissing)
	}

	title := item.Title
	if title == "" || strings.Contains(title, "://") {
		title = videoID
	}
	ext := "mp4"
	if d.audioOnly != "" {
		ext = d.audioOnly
	}
	outputPath := safeOutputPath(dest, title)
	outputPath = strings.TrimSuffix(outputPath, filepath.Ext(outputPath)) + "." + ext
	identity := youtubeOutputIdentity(videoID, d.quality, d.audioOnly)
	finalPath, err = claimYouTubeOutput(d.paths, outputPath, item.Key(), identity)
	if err != nil {
		return "", model.NewNonRetryable(err)
	}
	skip, err := prepareOutput(finalPath, identity)
	if err != nil || skip {
		return finalPath, err
	}

	workDir, err := streamWorkDir(dest, finalPath, identity+"\n"+BackendYTDLP)
	if err != nil {
		return "", model.NewNonRetryable(err)
	}
	defer func() { releaseWorkDir(workDir, err) }()

	cookieFile := ""
	if cookies := youtubeCookies(d.cookies); len(cookies) > 0 {
		cookieFile = filepath.Join(workDir, ytDLPCookieFile)
		if err = writeNetscapeCookies(cookieFile, cookies); err != nil {
			return "", model.NewNonRetryable(err)
		}
		defer os.Remove(cookieFile)
	}

	// #nosec G204 -- bin comes from PATH lookup of a fixed name; arguments
	// are fixed flags plus config values passed as separate argv entries
	// (no shell), and the URL is built from a validated video ID.
	cmd := exec.CommandContext(ctx, bin, d.ytDLPArgs(videoID, cookieFile)...)
	cmd.Dir = workDir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", model.NewNonRetryable(err)
	}
	if err = cmd.Start(); err != nil {
		return "", model.NewNonRetryable(fmt.Errorf("youtube: start yt-dlp: %w", err))
	}
	produced := scanYTDLPOutput(stdout, onProgress)
	if err = cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("youtube: yt-dlp failed: %w%s", err, lastErrorLine(&stderr))
	}
	if produced == "" {
		return "", fmt.Errorf("youtube: yt-dlp did not report an output file%s", lastErrorLine(&stderr))
	}
	if !filepath.IsAbs(produced) {
		produced = filepath.Join(workDir, produced)
	}
	if !sameDir(filepath.Dir(produced), workDir) {
		return "", model.NewNonRetryable(fmt.Errorf("youtube: yt-dlp wrote outside its work dir: %s", produced))
	}
	err = commitOutputForSource(produced, finalPath, d.Core.SkipSum(), "", identity)
	if err == nil {
		// A built-in attempt that fell back here may have left resumable
		// partials; with the output in place they are dead weight.
		_ = os.RemoveAll(workDirPath(dest, finalPath, identity))
	}
	return finalPath, err
}

// scanYTDLPOutput reports progress lines and returns the final file path.
// Video and audio download one after another, so finished files' sizes are
// carried forward to keep the progress monotonic.
func scanYTDLPOutput(r io.Reader, onProgress func(int64, int64)) string {
	var produced, current string
	var base, last, lastTotal int64
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if path, ok := strings.CutPrefix(line, ytDLPFile); ok {
			produced = strings.TrimSpace(path)
			continue
		}
		rest, ok := strings.CutPrefix(line, ytDLPProgress)
		if !ok || onProgress == nil {
			continue
		}
		fields := strings.SplitN(rest, " ", 4)
		if len(fields) < 3 {
			continue
		}
		file := ""
		if len(fields) == 4 {
			file = fields[3]
		}
		if file != current {
			if current != "" {
				base += max(last, lastTotal)
			}
			current, last, lastTotal = file, 0, 0
		}
		last = parseYTDLPNumber(fields[0])
		lastTotal = parseYTDLPNumber(fields[1])
		if lastTotal <= 0 {
			lastTotal = parseYTDLPNumber(fields[2])
		}
		onProgress(base+last, base+max(lastTotal, last))
	}
	_, _ = io.Copy(io.Discard, r)
	return produced
}

// sameDir compares directories after resolving symlinks (macOS /var is a
// symlink to /private/var, and yt-dlp may report either form).
func sameDir(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return ra == rb
}

// parseYTDLPNumber parses a progress field; yt-dlp prints "NA" for unknown
// values and floats for estimates.
func parseYTDLPNumber(s string) int64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 {
		return 0
	}
	return int64(f)
}

func lastErrorLine(stderr *bytes.Buffer) string {
	lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); strings.HasPrefix(line, "ERROR:") {
			return ": " + line
		}
	}
	if last := strings.TrimSpace(lines[len(lines)-1]); last != "" {
		return ": " + last
	}
	return ""
}

// youtubeCookies keeps only the cookies yt-dlp needs, so cookies for other
// sites never reach the cookie file handed to it.
func youtubeCookies(cookies []*http.Cookie) []*http.Cookie {
	var out []*http.Cookie
	for _, c := range cookies {
		domain := strings.ToLower(strings.TrimPrefix(c.Domain, "."))
		for _, site := range []string{"youtube.com", "google.com"} {
			if domain == site || strings.HasSuffix(domain, "."+site) {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

func writeNetscapeCookies(path string, cookies []*http.Cookie) error {
	var b strings.Builder
	b.WriteString("# Netscape HTTP Cookie File\n")
	for _, c := range cookies {
		includeSubdomains := "FALSE"
		if strings.HasPrefix(c.Domain, ".") {
			includeSubdomains = "TRUE"
		}
		cookiePath := c.Path
		if cookiePath == "" {
			cookiePath = "/"
		}
		var expiry int64
		if !c.Expires.IsZero() {
			expiry = c.Expires.Unix()
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\tTRUE\t%d\t%s\t%s\n", c.Domain, includeSubdomains, cookiePath, expiry, c.Name, c.Value)
	}
	f, err := fileutil.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
