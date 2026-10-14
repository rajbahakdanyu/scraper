package youtube

import (
	"fmt"
	"strings"

	ytdl "github.com/kkdai/youtube/v2"
)

// estimatedSize sums the content lengths of the formats selectFormats
// would pick, for display before any download starts. 0 if unknown.
func estimatedSize(video *ytdl.Video, maxHeight int) int64 {
	videoFmt, audioFmt := selectFormats(video, maxHeight)
	var size int64
	if videoFmt != nil {
		size += videoFmt.ContentLength
	}
	if audioFmt != nil {
		size += audioFmt.ContentLength
	}
	return size
}

// selectFormats picks the best video format (capped at maxHeight when set,
// 0 = no cap) and the best audio-only format. When maxHeight is set and
// no format fits, the displayable best is returned instead.
func selectFormats(video *ytdl.Video, maxHeight int) (videoFmt, audioFmt *ytdl.Format) {
	for i := range video.Formats {
		f := &video.Formats[i]
		if f.Height == 0 {
			continue
		}
		if maxHeight > 0 && f.Height > maxHeight {
			continue
		}
		if videoFmt == nil || f.Height > videoFmt.Height ||
			(f.Height == videoFmt.Height && f.Bitrate > videoFmt.Bitrate) {
			videoFmt = f
		}
	}
	if videoFmt == nil && maxHeight > 0 {
		videoFmt, _ = selectFormats(video, 0)
	}
	if videoFmt == nil || videoFmt.AudioChannels > 0 {
		return videoFmt, nil
	}
	audioFmt = selectBestAudio(video)
	return videoFmt, audioFmt
}

// selectBestAudio picks the best audio-only format (Height==0,
// AudioChannels>0): prefers the default audio track, then highest bitrate.
func selectBestAudio(video *ytdl.Video) *ytdl.Format {
	var best *ytdl.Format
	for i := range video.Formats {
		f := &video.Formats[i]
		if f.AudioChannels == 0 || f.Height != 0 {
			continue
		}
		if betterAudioFormat(f, best) {
			best = f
		}
	}
	if best == nil {
		for i := range video.Formats {
			f := &video.Formats[i]
			if f.AudioChannels > 0 && betterAudioFormat(f, best) {
				best = f
			}
		}
	}
	return best
}

func betterAudioFormat(candidate, current *ytdl.Format) bool {
	if current == nil {
		return true
	}
	candidateDefault := candidate.AudioTrack != nil && candidate.AudioTrack.AudioIsDefault
	currentDefault := current.AudioTrack != nil && current.AudioTrack.AudioIsDefault
	if candidateDefault != currentDefault {
		return candidateDefault
	}
	return candidate.Bitrate > current.Bitrate
}

// formatsForDownload validates the selected format pair. When audioOnly is
// true, videoFmt is always nil — only audio matters.
func formatsForDownload(video *ytdl.Video, maxHeight int, audioOnly bool) (vidFmt, audFmt *ytdl.Format, err error) {
	if audioOnly {
		audFmt = selectBestAudio(video)
		if audFmt == nil {
			vidFmt, _ = selectFormats(video, 0)
			if vidFmt == nil {
				return nil, nil, fmt.Errorf("youtube: no audio format found for %s", video.ID)
			}
			return vidFmt, nil, nil
		}
		return nil, audFmt, nil
	}
	vidFmt, audFmt = selectFormats(video, maxHeight)
	if vidFmt == nil {
		return nil, nil, fmt.Errorf("youtube: no video formats found for %s", video.ID)
	}
	if vidFmt.AudioChannels == 0 && audFmt == nil {
		return nil, nil, fmt.Errorf("youtube: no audio format found for %s", video.ID)
	}
	return vidFmt, audFmt, nil
}

// audioCopyable reports whether format's audio is AAC-in-mp4 (e.g.
// codecs="mp4a.40.2") and can therefore be stream-copied into the mp4
// output container. Anything else (opus in webm, ...) must be transcoded
// to AAC at mux time.
func audioCopyable(format *ytdl.Format) bool {
	if format == nil {
		return false
	}
	mime := strings.ToLower(format.MimeType)
	return strings.Contains(mime, "audio/mp4") && strings.Contains(mime, "mp4a")
}

var qualityHeights = map[string]int{
	"2160p": 2160,
	"1440p": 1440,
	"1080p": 1080,
	"720p":  720,
	"480p":  480,
	"360p":  360,
	"240p":  240,
}

// ValidQuality reports whether q is a supported --quality value and its
// height cap; "" (no cap) is valid with height 0.
func ValidQuality(q string) (int, bool) {
	if q == "" {
		return 0, true
	}
	height, ok := qualityHeights[q]
	return height, ok
}
