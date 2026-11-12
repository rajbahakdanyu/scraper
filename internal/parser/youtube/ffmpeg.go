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

// ffmpeg runs inside a work dir from newMuxWorkDir and only ever sees these
// fixed relative names, so no title, destination, or other outside string
// reaches its argument list. Inputs carry no extension: ffmpeg probes input
// containers by content; outputs keep one because it selects the muxer.
const (
	workVideo  = "video"
	workAudio  = "audio"
	workSource = "source"
	workMuxed  = "muxed.mp4"
	workM4A    = "audio.m4a"
	workMP3    = "audio.mp3"
)

// Every ffmpeg invocation starts with -nostdin (it would otherwise steal TUI
// keystrokes) and -loglevel error (stderr is kept only for error messages).
var (
	muxCopyArgs = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", workVideo, "-i", workAudio, "-c:v", "copy", "-c:a", "copy", "-movflags", "+faststart", workMuxed}
	muxTranscodeArgs = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", workVideo, "-i", workAudio, "-c:v", "copy", "-c:a", "aac", "-movflags", "+faststart", workMuxed}
	extractM4ACopyArgs = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", workSource, "-vn", "-acodec", "copy", workM4A}
	extractM4ATranscodeArgs = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", workSource, "-vn", "-c:a", "aac", workM4A}
	extractMP3Args = []string{"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-i", workSource, "-vn", "-codec:a", "libmp3lame", "-q:a", "2", workMP3}
)

// muxStreams combines workDir's video and audio streams into workDir's
// workMuxed file. Video is always stream-copied; audio is stream-copied
// when audioCopy is true (already AAC) and transcoded to AAC otherwise.
// ffmpeg's stderr is captured and its last line included in the returned
// error — a bare "exit status 1" tells the user nothing about why the mux
// failed.
func muxStreams(ctx context.Context, workDir string, audioCopy bool) error {
	args := muxTranscodeArgs
	if audioCopy {
		args = muxCopyArgs
	}
	return runFFmpeg("mux", workDir, exec.CommandContext(ctx, "ffmpeg", args...))
}

// extractAudio turns workDir's source stream into workM4A (stream copy) or,
// for format "mp3", workMP3 (libmp3lame transcode). It returns the name of
// the file it wrote inside workDir.
func extractAudio(ctx context.Context, workDir, format string, copyM4A bool) (string, error) {
	if format == "mp3" {
		return workMP3, runFFmpeg("extract audio", workDir, exec.CommandContext(ctx, "ffmpeg", extractMP3Args...))
	}
	args := extractM4ATranscodeArgs
	if copyM4A {
		args = extractM4ACopyArgs
	}
	return workM4A, runFFmpeg("extract audio", workDir, exec.CommandContext(ctx, "ffmpeg", args...))
}

func runFFmpeg(op, workDir string, cmd *exec.Cmd) error {
	var stderr bytes.Buffer
	cmd.Dir = workDir
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return ffmpegError(op, err, &stderr)
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
