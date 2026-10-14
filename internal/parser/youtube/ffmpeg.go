package youtube

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// newMuxWorkDir holds the separate streams before muxing. It lives beside
// the output, not in the system temp dir, which may be a small tmpfs or a
// different filesystem than a multi-GB download needs.
func newMuxWorkDir(dest string) (string, error) {
	return os.MkdirTemp(dest, ".scraper-yt-")
}

// ffmpegBaseArgs keep ffmpeg off the terminal: no stdin reads (it would
// steal TUI keystrokes) and only errors on stderr (kept for messages).
var ffmpegBaseArgs = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y"}

// muxStreams combines a separate video and audio stream into outputPath
// with ffmpeg. Video is always stream-copied; audio is stream-copied when
// audioCopy is true (already AAC) and transcoded to AAC otherwise.
// ffmpeg's stderr is captured and its last line included in the returned
// error — a bare "exit status 1" tells the user nothing about why the mux
// failed.
func muxStreams(ctx context.Context, videoPath, audioPath, outputPath string, audioCopy bool) error {
	var stderr bytes.Buffer
	audioCodec := "aac"
	if audioCopy {
		audioCodec = "copy"
	}
	args := append(append([]string(nil), ffmpegBaseArgs...),
		"-i", videoPath,
		"-i", audioPath,
		"-c:v", "copy",
		"-c:a", audioCodec,
		"-movflags", "+faststart",
		outputPath,
	)
	// #nosec G204 -- arguments are passed directly without shell evaluation; paths are internally allocated.
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ffmpegError("mux", err, &stderr)
	}
	return nil
}

// extractAudio extracts or transcodes audio from srcPath to outputPath.
// For m4a: stream-copies audio (-vn -acodec copy). For mp3: transcodes to
// mp3 via libmp3lame (-vn -acodec libmp3lame -q:a 2).
func extractAudio(ctx context.Context, srcPath, outputPath, format string) error {
	var stderr bytes.Buffer
	args := append(append([]string(nil), ffmpegBaseArgs...), "-i", srcPath, "-vn")
	if format == "mp3" {
		args = append(args, "-codec:a", "libmp3lame", "-q:a", "2")
	} else {
		args = append(args, "-acodec", "copy")
	}
	args = append(args, outputPath)
	// #nosec G204 -- arguments are passed without shell evaluation; paths are internally allocated.
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ffmpegError("extract audio", err, &stderr)
	}
	return nil
}

func ffmpegError(op string, err error, stderr *bytes.Buffer) error {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return model.NewNonRetryable(fmt.Errorf("youtube: failed to start ffmpeg: %w", err))
	}
	detail := strings.TrimSpace(stderr.String())
	if idx := strings.LastIndex(detail, "\n"); idx >= 0 {
		detail = detail[idx+1:]
	}
	if detail == "" {
		return fmt.Errorf("youtube: ffmpeg %s failed: %w", op, err)
	}
	return fmt.Errorf("youtube: ffmpeg %s failed: %w: %s", op, err, detail)
}
