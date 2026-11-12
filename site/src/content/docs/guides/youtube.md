---
title: YouTube
description: Download videos, playlists, and audio from YouTube.
---

Works with a single video or a playlist. Any URL with a `list=` parameter
downloads every video in the playlist.

```bash
scraper youtube https://youtu.be/VIDEO_ID
scraper youtube "https://www.youtube.com/playlist?list=PLAYLIST_ID"
```

## Quality

The highest-quality format is downloaded by default. `--quality` caps the
video resolution (`2160p`, `1440p`, `1080p`, `720p`, `480p`, `360p`, or
`240p`):

```bash
scraper --quality 1080p https://youtu.be/VIDEO_ID
```

If the chosen video format has no audio, the best audio track is downloaded
separately and muxed in with `ffmpeg`. Preview sizes are estimated from the
selected formats and may differ slightly from the final file.

## Audio only

```bash
scraper --audio-only https://youtu.be/VIDEO_ID       # m4a
scraper --audio-only mp3 https://youtu.be/VIDEO_ID
```

`m4a` stream-copies AAC when possible and transcodes other audio, such as
Opus, to AAC.

## yt-dlp fallback

If YouTube refuses a stream (HTTP 403 or 400), or the built-in downloader
can't read a video's formats, and [`yt-dlp`](https://github.com/yt-dlp/yt-dlp)
is on `PATH`, that video is retried with yt-dlp and a notice is shown.

| `--youtube-backend` | Behavior |
| --- | --- |
| `auto` (default) | Built-in downloader, falling back to yt-dlp |
| `builtin` | Built-in only; no fallback |
| `yt-dlp` | yt-dlp for every video |

yt-dlp receives the same quality, audio-only, proxy, and speed-limit
settings, plus any YouTube or Google cookies from `--cookies` or
`--cookies-from-browser`.

## Resuming

Interrupted streams resume after a retry or in a later run. Partial streams
wait in a hidden `.scraper-yt-*` folder beside the output and are reused only
if the video, format, size, and last-modified time still match. The folder is
removed once the video is saved; delete leftover `.scraper-yt-*` folders to
discard partial downloads you no longer want.
