# Scraper CLI

[![CI](https://github.com/rajbahakdanyu/scraper/actions/workflows/ci.yml/badge.svg)](https://github.com/rajbahakdanyu/scraper/actions/workflows/ci.yml)

A Go-based CLI tool for scraping and downloading media from YouTube,
CyberDrop, Bunker, GoonBox, and Filester — with a futuristic terminal UI that
launches instantly and shows live download progress.

## Requirements

- Go 1.26.5 or newer
- `ffmpeg` (YouTube downloads whose video and audio aren't already combined
  in one stream are muxed together automatically)

## Build

```bash
go build -o scraper ./cmd/scraper
```

Or install directly:

```bash
go install github.com/rajbahakdanyu/scraper/cmd/scraper@latest
```

## Usage

Every command launches the TUI immediately (unless `--dry-run`, `--no-tui`,
or `--json` is set — see below). The scraper runs in the background with a
loading spinner, then transitions to the download view.

The site can be auto-detected from the URL — just pass a bare URL with no
subcommand:

```bash
./scraper https://youtu.be/VIDEO_ID
./scraper https://filester.sh/f/FOLDER_ID
./scraper https://pixeldrain.com/l/CSbjLYKs
```

Or use an explicit subcommand (useful for scripting, or if detection ever
guesses wrong):

```bash
./scraper youtube https://youtu.be/VIDEO_ID
./scraper cyberdrop https://cyberdrop.cr/f/FILE_ID
./scraper bunker https://bunkr.cr/a/ALBUM_ID
./scraper goonbox https://goonbox.cr/a/ALBUM_ID
./scraper filester https://filester.sh/f/FOLDER_ID
./scraper filester https://filester.sh/d/FILE_SLUG
./scraper pixeldrain https://pixeldrain.com/l/CSbjLYKs
./scraper pixeldrain https://pixeldrain.com/u/qZz2NiiW
```
Preview what would be downloaded — item count and total size — without
downloading anything or launching the TUI:

```bash
./scraper --dry-run https://filester.sh/f/FOLDER_ID
```

### Batch mode

Pass multiple URLs to download them all in sequence (each scraped as it goes,
in the order given), instead of launching a fresh TUI per URL. All URLs must
be from the same site type — all YouTube, or all non-YouTube (which may mix
CyberDrop, Bunker, GoonBox, and Filester):

```bash
./scraper https://youtu.be/VIDEO_ID "https://www.youtube.com/playlist?list=ID"
./scraper https://cyberdrop.cr/f/FILE_ID https://bunkr.cr/a/ALBUM_ID
```

Or read URLs from a file, one per line, with `-i, --input-file`:

```bash
./scraper -i urls.txt
```

URLs given on stdin (a pipe) are also accepted when no positional arguments
or `--input-file` are present:

```bash
cat urls.txt | ./scraper
```

Each URL is detected from its host and downloads into its own album
subfolder under the output directory.

### Script-friendly output

For scripting and logs, `--no-tui` disables the terminal UI and prints plain
text lines as items are scraped and downloaded:

```bash
./scraper --no-tui https://youtu.be/VIDEO_ID
```

`-j, --json` outputs JSON Lines instead — one JSON event per line
(`album_start`, `album_error`, `item`, `summary`, `error`); it implies `--no-tui`. Combined
with `--dry-run`, it emits a single JSON document describing the albums
(URLs, items, sizes) with no download (batch mode emits one document per URL):

```bash
./scraper -j --dry-run https://filester.sh/f/FOLDER_ID
```

### YouTube

Works on both a single video URL and a playlist URL (anything with a
`list=` query parameter downloads every video in the playlist):

```bash
./scraper youtube https://youtu.be/VIDEO_ID
./scraper youtube "https://www.youtube.com/playlist?list=PLAYLIST_ID"
```

Always downloads the highest-quality format available. If that format
doesn't already include audio, the best available audio track is
downloaded separately and muxed in with ffmpeg.

### Albums (Bunker, GoonBox, CyberDrop, Filester)

All files in an album/folder are auto-downloaded concurrently into a
subfolder named after the album ID:

```
./downloads/
  k4OuX4pJ/        ← album subfolder
    image1.png
    video1.mp4
    ...
```

Single files (e.g. CyberDrop `/f/` links or Filester `/d/` links) download
directly to the output directory (or, in batch mode, into a slug subfolder
per URL — same as albums).

### Flags

| Flag             | Default | Description                                  |
|------------------|---------|-----------------------------------------------|
| `-u, --url`      | (none)  | Content URL (alternative to positional arg; subcommand-only) |
| `-o, --output`   | `~/Downloads` | Output directory (config default)                   |
| `--concurrent`   | `5`     | Maximum concurrent downloads                 |
| `--config`       | (none)  | Config file path (default: `~/.scraper.yaml`)|
| `-i, --input-file`| (none) | Read URLs from a file, one per line (batch mode) |
| `--limit-rate`   | (none)  | Cap total download speed (e.g. `500K`, `2M`, `1G`); unlimited by default |
| `--no-checksums` | `false` | Don't write/verify `.sha256` checksum sidecar files |
| `-n, --dry-run`  | `false` | List item count/total size without downloading anything |
| `--no-tui`       | `false` | Disable the terminal UI; print plain text output |
| `-j, --json`     | `false` | Output JSON Lines (one event per line); implies `--no-tui` |
| `--proxy`        | (none)  | HTTP/S proxy URL for all requests (e.g. `http://proxy:8080`) |
| `--cookies`      | (none)  | Netscape-format cookies file for HTTP requests |
| `--quality`      | (auto)  | Maximum YouTube quality (`2160p`, `1080p`, `720p`, `480p`, `360p`) |
| `--audio-only`   | `false` | Download audio tracks only (no video)                     |
| `--include`      | (none)  | Glob pattern(s) to filter items (e.g. `*.mp4,*.png`)      |
| `--exclude`      | (none)  | Glob pattern(s) to exclude items                          |
| `--min-size`     | (none)  | Minimum file size (e.g. `1K`, `10M`)                      |
| `--max-size`     | (none)  | Maximum file size                                         |
| `--disk-check`   | `true`  | Check free space before downloading                       |
| `--media-only`   | `true`  | Only download media/archive files (`--media-only=false` allows everything) |
| `--notify`       | `false` | Send desktop notification when download completes         |

Additional subcommands:

| Subcommand | Description                                      |
|------------|--------------------------------------------------|
| `version`  | Print the version                               |
| `completion [bash\|zsh\|fish\|powershell]` | Generate a shell completion script |

For example, to enable completions in Bash or Zsh:

```bash
source <(./scraper completion bash)
source <(./scraper completion zsh)
```

### TUI Keybindings

| Key             | Action                           |
|-----------------|-----------------------------------|
| `↑/k`, `↓/j`    | Navigate the item list           |
| `enter`         | Show the error for a failed item |
| `r`             | Retry the selected failed item   |
| `c`             | Cancel the selected item          |
| `q`, `esc`, `ctrl+c` | Quit                           |

### Media-only downloads (default)

By default the scraper only queues media and archive files. Items whose
filename (or, when the title carries no extension, the URL) ends in a known
media or archive extension are kept:

- video: mp4, m4v, mkv, webm, avi, mov, wmv, flv, mpg, mpeg, ts, m2ts, 3gp, ogv
- audio: mp3, m4a, aac, flac, wav, ogg, opus, wma
- image: jpg, jpeg, png, gif, webp, bmp, avif, heic, tiff
- archives: zip, rar, 7z, tar, gz, tgz, bz2, xz

Everything else — executables, scripts, HTML, PDFs, unknown or extensionless
files — is skipped before download, and the run prints
`media filter: N/M items (K skipped non-media)`. YouTube is unaffected.

Pass `--media-only=false` (or set `media_only: false` in the config) to
download everything an album lists.

This is a filename/MIME allowlist, not content verification: a payload
renamed to `video.mp4` still downloads, and archives are not inspected.

## Configuration

A YAML config file can be placed at `~/.scraper.yaml`:

```yaml
output_dir: ~/Downloads
concurrent_downloads: 10
timeout: 30s
user_agent: "Scraper/1.0"
rate_limit: 2M
skip_checksums: false
proxy: http://proxy:8080
cookies: ~/.scraper-cookies.txt
quality: "1080p"
audio_only: false
include: ["*.mp4", "*.mkv"]
exclude: ["*.txt"]
min_size: "1M"
max_size: "10G"
disk_check: true
media_only: true
notify: false
```

CLI flags override config file values. `--proxy`/`proxy` routes every
outbound request (scraping and downloads) through the given HTTP/S proxy.

Every completed download gets a `<file>.sha256` sidecar (standard
`sha256sum`-compatible format) next to it, unless `skip_checksums`/
`--no-checksums` is set. If a file with a matching sidecar is already fully
downloaded, re-running the same download skips it; if the sidecar no longer
matches (a corrupted file), it's redownloaded instead of trusted as-is.
`rate_limit`/`--limit-rate` caps the *combined* speed across all concurrent
downloads, not each one individually.

Interrupted direct HTTP downloads retain validated `.part` files and resume
with Range requests. YouTube streams restart after interruption because the
YouTube client library does not expose a stable entity validator/range contract.

## Supported Sites

| Site      | URL format                          | Notes                                |
|-----------|-------------------------------------|--------------------------------------|
| YouTube   | `youtu.be/ID`, `youtube.com/watch?v=ID`, or a `playlist?list=ID` URL | Always highest quality, auto-mux |
| CyberDrop | `cyberdrop.cr/f/SLUG` or `/a/SLUG` | API-based, signed CDN URLs           |
| Bunker    | `bunkr.cr/a/ALBUM_ID`              | API + token signing, parallel fetch  |
| GoonBox   | `goonbox.cr/a/ALBUM_ID`            | JSON API, paginated albums           |
| Filester  | `filester.sh/f/ID` or `/d/SLUG`    | HTML folder listing + signed download tokens, paginated |
| Pixeldrain| `pixeldrain.com/l/LIST_ID` or `/u/FILE_ID` | JSON API, direct download, Range-request resume |

Pixeldrain throttles download speed after ~6 GB per IP in a 24h window. To bypass this, use `--proxy` to switch IPs and regain full speed. Range-request resume also reuses partial downloads across runs, so an interrupted file picks up where it left off.

## Development

```bash
go build ./...
go vet ./...
go test ./...
```
