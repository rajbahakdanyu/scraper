---
title: Installation
description: Download a prebuilt release or build Scraper from source.
---

Scraper ships as two programs that share the same download engine and
settings file:

- **`scraper`**: the command-line tool and terminal dashboard.
- **`scraper-gui`**: the [desktop app](/scraper/guides/desktop-app/).

## Download a release

Every [GitHub release](https://github.com/rajbahakdanyu/scraper/releases/latest)
includes:

| Download | Platforms |
| --- | --- |
| `scraper-<os>-<arch>.tar.gz` | CLI for macOS (Intel and Apple Silicon), Linux (x86-64 and ARM64), and Windows (x86-64) |
| `scraper-gui-darwin-universal.zip` | Desktop app for macOS (Intel and Apple Silicon) |
| `scraper-gui-windows-amd64.zip` | Desktop app for Windows |
| `scraper-gui-linux-<arch>.tar.gz` | Desktop app for Linux (x86-64 and ARM64) |
| `scraper-tab-export-<version>.xpi` | [Firefox tab-export extension](/scraper/guides/browser-extension/) |

Each file has a matching `.sha256` checksum (the CLI archives share
`checksums.txt`).

Unless a release says it is signed, the desktop builds are unsigned:

- **macOS:** right-click the app and choose **Open** the first time.
- **Windows:** choose **More info → Run anyway**.
- **Linux:** install the GTK 4 and WebKitGTK 6 runtime (`libwebkitgtk-6.0-4`
  on Debian and Ubuntu).

## Runtime requirements

- **`ffmpeg`** on `PATH`, for YouTube downloads whose video and audio aren't
  already in one stream (they are muxed together automatically).
- Optional: [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) on `PATH`, used as a
  fallback for YouTube videos the built-in downloader can't fetch. See
  [YouTube](/scraper/guides/youtube/#yt-dlp-fallback).

## Install with Go

With Go 1.26.11 or newer:

```bash
go install github.com/rajbahakdanyu/scraper/cmd/scraper@latest
```

Or build from a clone:

```bash
go build -o scraper ./cmd/scraper
```

To build the desktop app yourself, see
[Contributing](/scraper/development/contributing/#desktop-app).

## Shell completion

```bash
source <(scraper completion bash)
source <(scraper completion zsh)
```

`fish` and `powershell` are also supported. `scraper version` prints the
installed version.
