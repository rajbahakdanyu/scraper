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
	return strings.TrimSpace(string(data))
}

func TestMuxStreamsSurfacesFFmpegStderr(t *testing.T) {
	stubFFmpeg(t, `echo "Error opening input: invalid data found" >&2; exit 1`)

	err := muxStreams(t.Context(), t.TempDir(), false)
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

	err := muxStreams(t.Context(), t.TempDir(), false)
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

	err := muxStreams(t.Context(), t.TempDir(), false)
	if err == nil {
		t.Fatal("expected an error when ffmpeg fails")
	}
	var nre *model.NonRetryableError
	if errors.As(err, &nre) {
		t.Fatalf("a mux failure must stay retryable, got %T: %v", err, err)
	}
}

func TestMuxStreamsRunsInsideTheWorkDir(t *testing.T) {
	stubFFmpeg(t, `touch muxed.mp4`)
	work := t.TempDir()

	if err := muxStreams(t.Context(), work, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, workMuxed)); err != nil {
		t.Fatalf("ffmpeg must run with the work dir as cwd so its relative output lands there: %v", err)
	}
}

func TestMuxStreamsCopiesAudioWhenAlreadyAAC(t *testing.T) {
	argsFile := stubFFmpeg(t, `exit 0`)

	if err := muxStreams(t.Context(), t.TempDir(), true); err != nil {
		t.Fatal(err)
	}
	want := "-nostdin -hide_banner -loglevel error -y -i video -i audio -c:v copy -c:a copy -movflags +faststart muxed.mp4"
	if got := readFFmpegArgs(t, argsFile); got != want {
		t.Fatalf("AAC audio must be stream-copied with only fixed relative names:\n got: %s\nwant: %s", got, want)
	}
}

func TestMuxStreamsTranscodesAudioWhenNotAAC(t *testing.T) {
	argsFile := stubFFmpeg(t, `exit 0`)

	if err := muxStreams(t.Context(), t.TempDir(), false); err != nil {
		t.Fatal(err)
	}
	want := "-nostdin -hide_banner -loglevel error -y -i video -i audio -c:v copy -c:a aac -movflags +faststart muxed.mp4"
	if got := readFFmpegArgs(t, argsFile); got != want {
		t.Fatalf("non-AAC audio must be transcoded to AAC for the mp4 container:\n got: %s\nwant: %s", got, want)
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

func TestExtractAudio(t *testing.T) {
	for _, tt := range []struct {
		format, file, args string
		copyM4A            bool
	}{
		{"m4a", workM4A, "-nostdin -hide_banner -loglevel error -y -i source -vn -acodec copy audio.m4a", true},
		{"m4a-opus", workM4A, "-nostdin -hide_banner -loglevel error -y -i source -vn -c:a aac audio.m4a", false},
		{"mp3", workMP3, "-nostdin -hide_banner -loglevel error -y -i source -vn -codec:a libmp3lame -q:a 2 audio.mp3", false},
	} {
		t.Run(tt.format, func(t *testing.T) {
			argsFile := stubFFmpeg(t, `output=""; for arg do output="$arg"; done; printf audio > "$output"`)
			work := t.TempDir()

			format := tt.format
			if format == "m4a-opus" {
				format = "m4a"
			}

			got, err := extractAudio(t.Context(), work, format, tt.copyM4A)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.file {
				t.Fatalf("extractAudio returned %q, want %q", got, tt.file)
			}
			if _, err := os.Stat(filepath.Join(work, got)); err != nil {
				t.Fatalf("extracted file missing from the work dir: %v", err)
			}
			if args := readFFmpegArgs(t, argsFile); args != tt.args {
				t.Fatalf("ffmpeg args:\n got: %s\nwant: %s", args, tt.args)
			}
		})
	}
}

func TestExtractM4ATranscodesOpusToAAC(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is not installed")
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Skip("ffprobe is not installed")
	}
	work := t.TempDir()
	source := exec.Command(ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "sine=frequency=1000:duration=1", "-c:a", "libopus",
		"-f", "webm", "-y", workSource)
	source.Dir = work
	if output, err := source.CombinedOutput(); err != nil {
		t.Skipf("ffmpeg cannot generate an Opus fixture: %v: %s", err, output)
	}
	if _, err := extractAudio(t.Context(), work, "m4a", false); err != nil {
		t.Fatalf("transcode Opus to M4A: %v", err)
	}
	probe := exec.Command(ffprobe, "-v", "error", "-select_streams", "a:0",
		"-show_entries", "stream=codec_name", "-of", "default=noprint_wrappers=1:nokey=1",
		filepath.Join(work, workM4A))
	codec, err := probe.Output()
	if err != nil {
		t.Fatalf("probe generated M4A: %v", err)
	}
	if strings.TrimSpace(string(codec)) != "aac" {
		t.Fatalf("M4A codec = %q, want AAC", strings.TrimSpace(string(codec)))
	}
}
