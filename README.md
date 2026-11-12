# Scraper

[![CI](https://github.com/rajbahakdanyu/scraper/actions/workflows/ci.yml/badge.svg)](https://github.com/rajbahakdanyu/scraper/actions/workflows/ci.yml)
[![Docs](https://img.shields.io/badge/docs-scraper.rajbahakdanyu.dev-4f46e5)](https://scraper.rajbahakdanyu.dev/)

Download media from YouTube, CyberDrop, Bunkr, GoonBox, Filester, Pixeldrain,
and Turbo, from a terminal dashboard that launches instantly or a desktop app
for macOS, Windows, and Linux. Albums download concurrently, interrupted
downloads resume, and failures are saved to a retry list you can replay with
one command.

**📖 Documentation: <https://scraper.rajbahakdanyu.dev/>**

## Install

Download the CLI or desktop app from the
[latest release](https://github.com/rajbahakdanyu/scraper/releases/latest), or
install the CLI with Go 1.26.11+:

```bash
go install github.com/rajbahakdanyu/scraper/cmd/scraper@latest
```

YouTube downloads need `ffmpeg` on `PATH`; [`yt-dlp`](https://github.com/yt-dlp/yt-dlp)
is an optional fallback. See
[Installation](https://scraper.rajbahakdanyu.dev/getting-started/installation/).

## Usage

```bash
scraper https://pixeldrain.com/l/CSbjLYKs           # site is auto-detected
scraper --dry-run https://filester.sh/f/FOLDER_ID   # preview count and size
scraper https://youtu.be/ID https://bunkr.cr/a/ID   # batch, mixed sites
scraper -i urls.txt                                 # batch from a file
```

- [Quick start](https://scraper.rajbahakdanyu.dev/getting-started/quick-start/)
- [Desktop app](https://scraper.rajbahakdanyu.dev/guides/desktop-app/)
- [Supported sites](https://scraper.rajbahakdanyu.dev/reference/sites/)
- [Flags](https://scraper.rajbahakdanyu.dev/reference/flags/) and
  [configuration](https://scraper.rajbahakdanyu.dev/reference/configuration/)
- [Retrying failures](https://scraper.rajbahakdanyu.dev/guides/retrying/)

## Development

```bash
go build ./... && go vet ./... && go test ./...
```

The desktop app (`gui/`), browser extension (`browser-extension/`), and docs
site (`site/`) have their own toolchains; see
[Contributing](https://scraper.rajbahakdanyu.dev/development/contributing/)
and [Releasing](https://scraper.rajbahakdanyu.dev/development/releasing/).
