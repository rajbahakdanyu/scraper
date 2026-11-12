# Scraper

[![CI](https://github.com/rajbahakdanyu/scraper/actions/workflows/ci.yml/badge.svg)](https://github.com/rajbahakdanyu/scraper/actions/workflows/ci.yml)
[![Docs](https://img.shields.io/badge/docs-rajbahakdanyu.github.io%2Fscraper-4f46e5)](https://rajbahakdanyu.github.io/scraper/)

Download media from YouTube, CyberDrop, Bunkr, GoonBox, Filester, Pixeldrain,
and Turbo, from a terminal dashboard that launches instantly or a desktop app
for macOS, Windows, and Linux. Albums download concurrently, interrupted
downloads resume, and failures are saved to a retry list you can replay with
one command.

**📖 Documentation: <https://rajbahakdanyu.github.io/scraper/>**

## Install

Download the CLI or desktop app from the
[latest release](https://github.com/rajbahakdanyu/scraper/releases/latest), or
install the CLI with Go 1.26.11+:

```bash
go install github.com/rajbahakdanyu/scraper/cmd/scraper@latest
```

YouTube downloads need `ffmpeg` on `PATH`; [`yt-dlp`](https://github.com/yt-dlp/yt-dlp)
is an optional fallback. See
[Installation](https://rajbahakdanyu.github.io/scraper/getting-started/installation/).

## Usage

```bash
scraper https://pixeldrain.com/l/CSbjLYKs           # site is auto-detected
scraper --dry-run https://filester.sh/f/FOLDER_ID   # preview count and size
scraper https://youtu.be/ID https://bunkr.cr/a/ID   # batch, mixed sites
scraper -i urls.txt                                 # batch from a file
```

- [Quick start](https://rajbahakdanyu.github.io/scraper/getting-started/quick-start/)
- [Desktop app](https://rajbahakdanyu.github.io/scraper/guides/desktop-app/)
- [Supported sites](https://rajbahakdanyu.github.io/scraper/reference/sites/)
- [Flags](https://rajbahakdanyu.github.io/scraper/reference/flags/) and
  [configuration](https://rajbahakdanyu.github.io/scraper/reference/configuration/)
- [Retrying failures](https://rajbahakdanyu.github.io/scraper/guides/retrying/)

## Development

```bash
go build ./... && go vet ./... && go test ./...
```

The desktop app (`gui/`), browser extension (`browser-extension/`), and docs
site (`site/`) have their own toolchains; see
[Contributing](https://rajbahakdanyu.github.io/scraper/development/contributing/)
and [Releasing](https://rajbahakdanyu.github.io/scraper/development/releasing/).
