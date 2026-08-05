# Spec: YouTube Quality & Audio Controls

**Date:** 2026-08-06
**Status:** approved
**Scope:** Chunk C — add `--quality` and `--audio-only` flags for YouTube downloads.

## Requirements

**P1.** `--quality <value>` persistent CLI flag. Accepted values: `2160p`, `1440p`, `1080p`, `720p`, `480p`, `360p`, `240p`. Invalid value → fatal at startup. Also settable via config YAML (`quality: 1080p`). CLI overrides config (same `flags.Changed` pattern). Default (flag unset): current behavior — pick highest resolution available.

**P2.** `--audio-only[=m4a|mp3]` persistent CLI flag. No value defaults to `m4a`. Accepted values: `m4a`, `mp3`. Invalid value → fatal at startup. Also settable via config YAML (`audio_only: m4a`). CLI overrides config.

**P3.** `--quality` and `--audio-only` are mutually exclusive. Setting both → fatal at startup: `cannot use --quality and --audio-only together`.

**P4.** Quality selection algorithm:
- From formats with `Height > 0` AND `Height ≤ requested`, pick highest bitrate.
- If the best available height is below the requested quality, download at that height and warn to stderr: `youtube: quality 1080p not available, falling back to 480p`.
- When `--quality` is unset, current behavior is unchanged (pick highest height, tie-break by bitrate).
- Audio track selection is unchanged regardless of quality cap (best audio-only format, preferring default audio track, tie-break by bitrate).

**P5.** Audio-only behavior:
- `m4a` (default): download best audio-only format directly as `.m4a`. No transcoding.
- `mp3`: download best audio-only format, transcode to `.mp3` via ffmpeg: `ffmpeg -i in -codec:a libmp3lame -q:a 2 out.mp3`.
- When the only available stream is muxed (video+audio in one format): download the full stream, extract audio via ffmpeg: `ffmpeg -i in -vn -acodec copy out.m4a` for m4a, or transcode for mp3.

**P6.** Quality/audio flags take effect only when `siteName == "youtube"` (in `runWithScraper` and `runBatch`). Non-YouTube scrapers ignore them — no error, just silently not applicable.

**P7.** TUI and script-friendly (`--no-tui`/`--json`) output modes both respect these flags. JSON events include `quality` and `audioOnly` fields when set.

## Design

### Changed: `internal/parser/youtube/youtube.go`

`selectBestFormats` renamed/replaced with `selectFormats(video *ytdl.Video, maxHeight int)`:

```
func selectFormats(video *ytdl.Video, maxHeight int) (videoFmt, audioFmt *ytdl.Format) {
    // Filter: Height > 0, Height ≤ maxHeight (or all if maxHeight == 0)
    // Pick: highest height, tie-break by bitrate
    // Fallback: if maxHeight > 0 and no format ≤ maxHeight, pick best available + warn stderr
    // Audio: unchanged — best audio-only format (height==0, AudioChannels>0, default track, bitrate)
}
```

`formatsForDownload` gains `maxHeight int` and `audioOnly bool` parameters:
```
func formatsForDownload(video *ytdl.Video, maxHeight int, audioOnly bool) (videoFmt, audioFmt *ytdl.Format, err error)
```

New: `selectBestAudio(video *ytdl.Video) *ytdl.Format` — picks best audio-only format by default audio track flag then bitrate.

`downloadBest` gains `maxHeight int, audioOnly string` parameters. When `audioOnly != ""`, calls `selectBestAudio`, skips video download. For mp3, after audio download, transcode via ffmpeg.

`DownloaderConfig` gains:
```
Quality   string `yaml:"quality"`     // "" or "1080p" etc.
AudioOnly string `yaml:"audio_only"`  // "" or "m4a"/"mp3"
```

### Changed: `internal/config/config.go`

```
type Config struct {
    // ... existing ...
    Quality   string `yaml:"quality"`
    AudioOnly string `yaml:"audio_only"`
}
```

### Changed: `cmd/root.go`

New persistent flags:
```
root.PersistentFlags().String("quality", "", "Max YouTube quality (2160p, 1440p, 1080p, 720p, 480p, 360p, 240p)")
root.PersistentFlags().String("audio-only", "", "YouTube audio only; optional format: m4a (default) or mp3")
```

`globalOptions` gains `quality/qualitySet` and `audioOnly/audioOnlySet`.

`loadGlobalConfig`: if `opts.qualitySet`, `cfg.Quality = opts.quality`. If `opts.audioOnlySet`, `cfg.AudioOnly = opts.audioOnly`. Mutual exclusion check: error if both non-empty.

`runWithScraper` and `runBatch`: pass `cfg.Quality` and `cfg.AudioOnly` to `youtube.DownloaderConfig`.

### Error handling

| Scenario | Behavior |
|---|---|
| `--quality 9999p` | Fatal: `invalid --quality value "9999p"` |
| `--audio-only --quality 1080p` | Fatal: `cannot use --quality and --audio-only together` |
| `--audio-only=ogg` | Fatal: `invalid --audio-only value "ogg" (valid: m4a, mp3)` |
| Quality > best available | Fall back to best available + stderr warning |
| Quality set, no formats at all | Existing error: `no video formats found` |
| Audio-only, no audio format | Fatal: `no audio format found` |

### Testing

- `internal/parser/youtube/youtube_test.go`: selectFormats with cap, selectBestAudio, fallback warning
- `cmd/root_test.go`: flag existence, mutual exclusion, config override, invalid value errors
