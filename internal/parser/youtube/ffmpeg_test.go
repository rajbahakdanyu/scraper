package youtube

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rajbahakdanyu/scraper/internal/model"
)

// stubFFmpeg puts an executable named "ffmpeg" on PATH that runs the given
// shell script body, so mux code can be tested without a real ffmpeg. The
// stub records its arguments to the file named in $FFMPEG_ARGS_FILE.
func stubFFmpeg(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	full := "#!/bin/sh\n" + `echo "$@" > "$FFMPEG_ARGS_FILE"` + "\n" + script + "\n"
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(full), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FFMPEG_ARGS_FILE", argsFile)
	return argsFile
}

func readFFmpegArgs(t *testing.T, argsFile string) string {
	t.Helper()
	data, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestMuxStreamsSurfacesFFmpegStderr(t *testing.T) {
	stubFFmpeg(t, `echo "Error opening input: invalid data found" >&2; exit 1`)

	err := muxStreams(t.Context(), "video.mp4", "audio.m4a", "out.part.mp4", false)
	if err == nil {
		t.Fatal("expected an error when ffmpeg fails")
	}
	if !strings.Contains(err.Error(), "invalid data found") {
		t.Fatalf("ffmpeg's stderr must be included in the error, got: %v", err)
	}
}

func TestMuxStreamsMissingFFmpegIsPermanent(t *testing.T) {
	// No ffmpeg on PATH at all: the exec-startup failure must be
	// classified permanent — no amount of retrying installs ffmpeg.
	t.Setenv("PATH", t.TempDir())

	err := muxStreams(t.Context(), "video.mp4", "audio.m4a", "out.part.mp4", false)
	if err == nil {
		t.Fatal("expected an error when ffmpeg is not installed")
	}
	var nre *model.NonRetryableError
	if !errors.As(err, &nre) {
		t.Fatalf("a missing ffmpeg binary must be permanent (not retried), got %T: %v", err, err)
	}
}

func TestMuxStreamsMuxFailureStaysRetryable(t *testing.T) {
	// ffmpeg started fine but rejected the inputs (e.g. a truncated or
	// corrupt stream): a retry downloads fresh bytes and can succeed, so
	// a mux failure must stay retryable.
	stubFFmpeg(t, `exit 1`)

	err := muxStreams(t.Context(), "video.mp4", "audio.m4a", "out.part.mp4", false)
	if err == nil {
		t.Fatal("expected an error when ffmpeg fails")
	}
	var nre *model.NonRetryableError
	if errors.As(err, &nre) {
		t.Fatalf("a mux failure must stay retryable, got %T: %v", err, err)
	}
}

func TestMuxStreamsSucceedsWhenFFmpegDoes(t *testing.T) {
	stubFFmpeg(t, `exit 0`)

	if err := muxStreams(t.Context(), "video.mp4", "audio.m4a", "out.part.mp4", false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMuxStreamsCopiesAudioWhenAlreadyAAC(t *testing.T) {
	argsFile := stubFFmpeg(t, `exit 0`)

	if err := muxStreams(t.Context(), "video.mp4", "audio.m4a", "out.part.mp4", true); err != nil {
		t.Fatal(err)
	}
	args := readFFmpegArgs(t, argsFile)
	if !strings.Contains(args, "-c:a copy") {
		t.Fatalf("AAC audio must be stream-copied (lossless, fast), got args: %s", args)
	}
	if !strings.HasPrefix(args, "-nostdin -hide_banner -loglevel error -y ") {
		t.Fatalf("ffmpeg must not read stdin or flood stderr, got args: %s", args)
	}
}

func TestMuxWorkDirIsBesideTheOutput(t *testing.T) {
	dest := t.TempDir()
	dir, err := newMuxWorkDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(dir) != dest {
		t.Fatalf("mux work dir %q must be inside %q", dir, dest)
	}
}

func TestMuxStreamsTranscodesAudioWhenNotAAC(t *testing.T) {
	argsFile := stubFFmpeg(t, `exit 0`)

	if err := muxStreams(t.Context(), "video.webm", "audio.webm", "out.part.mp4", false); err != nil {
		t.Fatal(err)
	}
	args := readFFmpegArgs(t, argsFile)
	if !strings.Contains(args, "-c:a aac") {
		t.Fatalf("non-AAC audio must be transcoded to AAC for the mp4 container, got args: %s", args)
	}
}

func TestExtractAudioM4a(t *testing.T) {
	// When ffmpeg is available, verify m4a extraction copies audio codec
	// (lossless pass-through: -vn -acodec copy)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	stubFFmpeg(t, `
ext="$(echo "$*" | grep -o '[^ ]*\.m4a$' || true)"
if echo "$*" | grep -q '\-acodec copy'; then
  touch "$ext"
  echo "copied" >&2
fi
exit 0
`)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	if err := os.WriteFile(src, []byte("video+audio"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.m4a")
	if err := extractAudio(t.Context(), src, out, "m4a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal("expected output file to exist")
	}
}

func TestExtractAudioMp3(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	stubFFmpeg(t, `
ext="$(echo "$*" | grep -o '[^ ]*\.mp3$' || true)"
if echo "$*" | grep -q 'libmp3lame'; then
  touch "$ext"
  echo "transcoded" >&2
fi
exit 0
`)
	dir := t.TempDir()
	src := filepath.Join(dir, "src.m4a")
	if err := os.WriteFile(src, []byte("audio"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp3")
	if err := extractAudio(t.Context(), src, out, "mp3"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal("expected mp3 output file to exist")
	}
}

func TestExtractAudioMp3FromMuxedStream(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available")
	}
	stubFFmpeg(t, `
if echo "$*" | grep -q 'libmp3lame' && echo "$*" | grep -q '\-vn'; then
  ext="$(echo "$*" | grep -o '[^ ]*\.mp3$')"
  touch "$ext"
fi
exit 0
`)
	dir := t.TempDir()
	src := filepath.Join(dir, "muxed.mp4")
	if err := os.WriteFile(src, []byte("muxed"), 0644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out.mp3")
	if err := extractAudio(t.Context(), src, out, "mp3"); err != nil {
		t.Fatal(err)
	}
}
