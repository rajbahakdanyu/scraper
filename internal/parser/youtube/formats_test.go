package youtube

import (
	"strings"
	"testing"

	ytdl "github.com/kkdai/youtube/v2"
)

func TestSelectFormatsPicksHighestResolution(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 360, Bitrate: 500},
			{Height: 1080, Bitrate: 4000},
			{Height: 720, Bitrate: 2000},
			{Height: 0, Bitrate: 128, AudioChannels: 2}, // audio-only
			{Height: 0, Bitrate: 256, AudioChannels: 2}, // audio-only, higher bitrate
		},
	}
	videoFmt, audioFmt := selectFormats(video, 0)
	if videoFmt == nil || videoFmt.Height != 1080 {
		t.Fatalf("expected 1080p video format, got %+v", videoFmt)
	}
	if audioFmt == nil || audioFmt.Bitrate != 256 {
		t.Fatalf("expected the 256-bitrate audio format, got %+v", audioFmt)
	}
}

func TestSelectFormatsPrefersDefaultAudioLanguage(t *testing.T) {
	defaultTrack := &struct {
		DisplayName    string `json:"displayName"`
		ID             string `json:"id"`
		AudioIsDefault bool   `json:"audioIsDefault"`
	}{DisplayName: "English", AudioIsDefault: true}
	nonDefaultTrack := &struct {
		DisplayName    string `json:"displayName"`
		ID             string `json:"id"`
		AudioIsDefault bool   `json:"audioIsDefault"`
	}{DisplayName: "Spanish", AudioIsDefault: false}
	video := &ytdl.Video{Formats: ytdl.FormatList{
		{Height: 1080, Bitrate: 4000},
		{Bitrate: 128, AudioChannels: 2, AudioTrack: defaultTrack},
		{Bitrate: 256, AudioChannels: 2, AudioTrack: nonDefaultTrack},
	}}

	_, audioFmt := selectFormats(video, 0)
	if audioFmt == nil || audioFmt.AudioTrack == nil || !audioFmt.AudioTrack.AudioIsDefault {
		t.Fatalf("selected non-default audio: %+v", audioFmt)
	}
}

func TestFormatsForDownloadRejectsMuteVideo(t *testing.T) {
	video := &ytdl.Video{ID: "mute", Formats: ytdl.FormatList{{Height: 1080, Bitrate: 4000}}}
	if _, _, err := formatsForDownload(video, 0, false); err == nil || !strings.Contains(err.Error(), "no audio format") {
		t.Fatalf("expected no-audio error, got %v", err)
	}
}

func TestSelectFormatsSkipsAudioWhenAlreadyMuxed(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000, AudioChannels: 2}, // already muxed
			{Height: 0, Bitrate: 256, AudioChannels: 2},
		},
	}
	videoFmt, audioFmt := selectFormats(video, 0)
	if videoFmt == nil || videoFmt.Height != 1080 {
		t.Fatalf("expected 1080p video format, got %+v", videoFmt)
	}
	if audioFmt != nil {
		t.Errorf("expected no separate audio format for an already-muxed stream, got %+v", audioFmt)
	}
}

func TestSelectFormatsBreaksHeightTiesByBitrate(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 3000},
			{Height: 1080, Bitrate: 5000},
		},
	}
	videoFmt, _ := selectFormats(video, 0)
	if videoFmt == nil || videoFmt.Bitrate != 5000 {
		t.Fatalf("expected the higher-bitrate 1080p format, got %+v", videoFmt)
	}
}

func TestSelectFormatsNoFormats(t *testing.T) {
	videoFmt, audioFmt := selectFormats(&ytdl.Video{}, 0)
	if videoFmt != nil || audioFmt != nil {
		t.Errorf("expected nil, nil for a video with no formats, got %+v, %+v", videoFmt, audioFmt)
	}
}

func TestEstimatedSizeSumsVideoAndAudio(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, ContentLength: 1000},
			{Height: 0, AudioChannels: 2, ContentLength: 200},
		},
	}
	if got := estimatedSize(video, 0); got != 1200 {
		t.Errorf("estimatedSize = %d, want 1200", got)
	}
}

func TestAudioCopyable(t *testing.T) {
	aac := &ytdl.Format{MimeType: `audio/mp4; codecs="mp4a.40.2"`}
	opus := &ytdl.Format{MimeType: `audio/webm; codecs="opus"`}
	if !audioCopyable(aac) {
		t.Error("audio/mp4 (AAC) should be copyable into the mp4 container")
	}
	if audioCopyable(opus) {
		t.Error("audio/webm (opus) must be transcoded, not copied")
	}
}

func TestSelectFormatsQualityCap(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000},
			{Height: 720, Bitrate: 2000},
			{Height: 480, Bitrate: 1000},
			{Height: 0, Bitrate: 128, AudioChannels: 2},
		},
	}
	videoFmt, _ := selectFormats(video, 720)
	if videoFmt == nil || videoFmt.Height != 720 {
		t.Fatalf("expected 720p with quality cap, got %+v", videoFmt)
	}
}

func TestSelectFormatsMaxHeightZeroIsUncapped(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000},
			{Height: 720, Bitrate: 2000},
		},
	}
	videoFmt, _ := selectFormats(video, 0)
	if videoFmt == nil || videoFmt.Height != 1080 {
		t.Fatalf("expected 1080p (uncapped), got %+v", videoFmt)
	}
}

func TestSelectFormatsHeightTieBreakByBitrate(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 720, Bitrate: 2000},
			{Height: 720, Bitrate: 4000},
		},
	}
	videoFmt, _ := selectFormats(video, 720)
	if videoFmt == nil || videoFmt.Bitrate != 4000 {
		t.Fatalf("expected higher-bitrate 720p, got %+v", videoFmt)
	}
}

func TestSelectFormatsQualityFallback(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 480, Bitrate: 1000},
			{Height: 360, Bitrate: 500},
		},
	}
	videoFmt, _ := selectFormats(video, 1080)
	if videoFmt == nil || videoFmt.Height != 480 {
		t.Fatalf("expected fallback to 480p, got %+v", videoFmt)
	}
}

func TestSelectBestAudio(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000},
			{Height: 0, Bitrate: 128, AudioChannels: 2},
			{Height: 0, Bitrate: 256, AudioChannels: 2},
		},
	}
	audioFmt := selectBestAudio(video)
	if audioFmt == nil || audioFmt.Bitrate != 256 {
		t.Fatalf("expected 256-bitrate audio, got %+v", audioFmt)
	}
}

func TestSelectBestAudioPrefersDefaultTrack(t *testing.T) {
	defaultTrack := &struct {
		DisplayName    string `json:"displayName"`
		ID             string `json:"id"`
		AudioIsDefault bool   `json:"audioIsDefault"`
	}{DisplayName: "English", AudioIsDefault: true}
	nonDefaultTrack := &struct {
		DisplayName    string `json:"displayName"`
		ID             string `json:"id"`
		AudioIsDefault bool   `json:"audioIsDefault"`
	}{DisplayName: "Spanish", AudioIsDefault: false}
	video := &ytdl.Video{Formats: ytdl.FormatList{
		{Height: 0, Bitrate: 128, AudioChannels: 2, AudioTrack: nonDefaultTrack},
		{Height: 0, Bitrate: 64, AudioChannels: 2, AudioTrack: defaultTrack},
	}}
	audioFmt := selectBestAudio(video)
	if audioFmt == nil || audioFmt.AudioTrack == nil || !audioFmt.AudioTrack.AudioIsDefault {
		t.Fatalf("expected default audio track, got %+v", audioFmt)
	}
}

func TestSelectBestAudioNoAudioFormats(t *testing.T) {
	video := &ytdl.Video{
		Formats: ytdl.FormatList{
			{Height: 1080, Bitrate: 4000},
		},
	}
	audioFmt := selectBestAudio(video)
	if audioFmt != nil {
		t.Fatalf("expected nil when no audio-only formats, got %+v", audioFmt)
	}
}

func TestFormatsForDownloadAudioOnly(t *testing.T) {
	video := &ytdl.Video{ID: "test", Formats: ytdl.FormatList{
		{Height: 1080, Bitrate: 4000},
		{Height: 0, Bitrate: 128, AudioChannels: 2},
	}}
	videoFmt, audioFmt, err := formatsForDownload(video, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if videoFmt != nil {
		t.Fatalf("expected nil videoFmt for audio-only, got %+v", videoFmt)
	}
	if audioFmt == nil || audioFmt.Bitrate != 128 {
		t.Fatalf("expected audio format for audio-only, got %+v", audioFmt)
	}
}

func TestFormatsForDownloadAudioOnlyNoAudio(t *testing.T) {
	video := &ytdl.Video{ID: "mute", Formats: ytdl.FormatList{
		{Height: 1080, Bitrate: 4000},
	}}
	vidFmt, audFmt, err := formatsForDownload(video, 0, true)
	if err != nil {
		t.Fatalf("expected fallback to muxed format for audio-only, got error: %v", err)
	}
	if vidFmt == nil {
		t.Fatal("expected vidFmt (muxed fallback) for audio-only")
	}
	if audFmt != nil {
		t.Fatalf("expected nil audFmt for muxed fallback, got %+v", audFmt)
	}
}
