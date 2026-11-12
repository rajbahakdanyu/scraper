# Scraper CLI

[![CI](https://github.com/rajbahakdanyu/scraper/actions/workflows/ci.yml/badge.svg)](https://github.com/rajbahakdanyu/scraper/actions/workflows/ci.yml)

A Go-based CLI tool for scraping and downloading media from YouTube,
CyberDrop, Bunker, GoonBox, Filester, Pixeldrain, and Turbo — with a futuristic terminal UI that
launches instantly and shows live download progress, and a
[desktop GUI](#desktop-gui) with the same features.

## Requirements

- Go 1.26.11 or newer
- `ffmpeg` (YouTube downloads whose video and audio aren't already combined
  in one stream are muxed together automatically)
- Optional: [`yt-dlp`](https://github.com/yt-dlp/yt-dlp) on `PATH`, used as a
  fallback for YouTube videos the built-in downloader can't fetch

## Build

```bash
go build -o scraper ./cmd/scraper
```

Or install directly:

```bash
go install github.com/rajbahakdanyu/scraper/cmd/scraper@latest
```

## Desktop GUI

`scraper-gui` is a desktop app ([Wails](https://wails.io) + Svelte) built on
the same scraping, download, and run-state code as the TUI. Release builds
for macOS (universal), Windows, and Linux are attached to each
[release](https://github.com/rajbahakdanyu/scraper/releases). They are
unsigned: on macOS, right-click the app and choose **Open** the first time;
on Windows, choose **More info → Run anyway**. Linux needs the WebKitGTK 4.1
runtime (`libwebkit2gtk-4.1-0` on Debian/Ubuntu).

The GUI reads and writes the same `~/.scraper.yaml` as the CLI. Settings can
be applied to the next run only, or saved to the file (comments and unknown
keys are kept).

| TUI / CLI | GUI |
|-----------|-----|
| `scraper <url>`, per-site subcommands | Paste URLs; **Site** forces the site for a single URL |
| Batch args, `-i/--input-file`, stdin, retry lists | Paste several URLs or **Import list…** (always runs as a batch) |
| `--dry-run` | **Preview** (items, sizes, unknown sizes, disk warning) |
| `--watch` | **Watch every** interval, **Stop watching** |
| Flags and config keys | **Settings** (every config key) |
| Dashboard: progress bar, stats, speed, ETA, album headers | Run view with the same totals, grouped, foldable list |
| `c` `C` `r` `R` `F` `/` `n` `f` `Tab` `e` `enter` | Same keys, plus row buttons and filter chips |
| Summary: `enter`, `R`, `L`, `q` | Summary dialog with the same actions |
| Retry list written on exit | Written when each group closes; **Show** reveals it |
| Warnings after the TUI exits | **Notices** panel |
| `--notify`, `--log-file` | Same settings |

The GUI also opens the output folder, reveals finished files, and copies
item URLs.

To build it yourself you need Node.js `^22.22.2 || ^24.15.0 || >=26.0.0`
(`gui/frontend/.nvmrc` pins 24; npm refuses older versions), the
[Wails v2 CLI](https://wails.io/docs/gettingstarted/installation), and on
Linux `libgtk-3-dev libwebkit2gtk-4.1-dev`:

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1
cd gui
wails build                    # Linux: wails build -tags webkit2_41
# app in gui/build/bin/
```

`wails dev` (in `gui/`) runs it with hot reload and also serves the UI at
<http://localhost:34115> with live Go bindings.

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
./scraper turbo https://turbo.cr/a/ALBUM_ID
./scraper turbo https://turbo.cr/v/FILE_ID
```
Preview what would be downloaded — item count and total size — without
downloading anything or launching the TUI:

```bash
./scraper --dry-run https://filester.sh/f/FOLDER_ID
```

### Batch mode

Pass multiple URLs to download them all in sequence (each scraped as it goes,
in the order given), instead of launching a fresh TUI per URL. Site types can
be mixed freely — YouTube links run first as their own group, then everything
else (CyberDrop, Bunker, GoonBox, Filester, ...) — and the non-YouTube group
may itself mix sites:

```bash
./scraper https://youtu.be/VIDEO_ID "https://www.youtube.com/playlist?list=ID"
./scraper https://cyberdrop.cr/f/FILE_ID https://bunkr.cr/a/ALBUM_ID
./scraper https://youtu.be/VIDEO_ID https://bunkr.cr/a/ALBUM_ID
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

#### Dashboard keys

Keys are case-sensitive. Progress and statistics always cover the whole run,
including items hidden by a filter or folded album. Completed albums fold
automatically; albums with failures stay open, and manually unfolded albums
remain open.

| Key | Action |
|-----|--------|
| `↑/↓`, `j/k`, mouse wheel | Select an item or folded album |
| `c` | Cancel the selected queued or active download |
| `C` | Cancel all queued and active downloads |
| `r` | Retry the selected failed download |
| `R` | Retry all failures; from the summary, return to the download list |
| `F` | Jump to the next failed item |
| `/` | Start a title search; `enter` accepts it and `esc` clears it |
| `n` | Jump to the next search match |
| `enter` | Show the selected failed item's error outside search; from the summary, view the file list |
| `f` | Cycle all, failed, active, and pending items |
| `Tab` | Fold or unfold the current album |
| `e` | Show scrape errors |
| `L` | Write the retry list from the end-of-run summary |
| `q`, `esc`, `ctrl+c` | Quit outside search |

The summary appears only after every album has been scraped and every
download has finished. It reports completed, failed, and cancelled counts,
and accepts only `enter`, `R`, `L`, and the quit keys. Retrying from the file
list brings the summary back when the retried downloads finish.
Unknown file sizes mark the overall byte percentage with `~` until their
sizes are known.

### Script-friendly output

For scripting and logs, `--no-tui` disables the terminal UI and prints plain
text lines as items are scraped and downloaded:

```bash
./scraper --no-tui https://youtu.be/VIDEO_ID
```

`-j, --json` outputs JSON Lines instead — one JSON event per line
(`album_start`, `album_error`, `scrape_warning`, `item`, `summary`, `error`); it implies `--no-tui`. Combined
with `--dry-run`, it emits a single JSON document describing the albums
(URLs, items, sizes) with no download (batch mode emits one document per URL):

```bash
./scraper -j --dry-run https://filester.sh/f/FOLDER_ID
```

Exit status is the same in every mode (TUI, `--no-tui`, `--json`): `0` when
everything downloaded, `1` when the run failed outright (e.g. the URL could not
be scraped), and `2` when a scrape returned partial results or only some items
or albums failed. A partial dry-run includes the available items and reports
the scrape warning; it exits with status `2`.

### YouTube

Works on both a single video URL and a playlist URL (anything with a
`list=` query parameter downloads every video in the playlist):

```bash
./scraper youtube https://youtu.be/VIDEO_ID
./scraper youtube "https://www.youtube.com/playlist?list=PLAYLIST_ID"
```

Downloads the highest-quality format available by default; `--quality` caps
the video resolution. Preview sizes use the selected video/audio formats as
an estimate, and may differ from the final size after muxing or audio
transcoding. If the selected video format doesn't already include audio, the
best available audio track is downloaded separately and muxed in with ffmpeg.
`--audio-only m4a` stream-copies AAC when possible and transcodes incompatible
audio such as Opus to AAC.

If YouTube refuses a stream (HTTP 403/400) or the built-in downloader can't
read a video's formats or info, and `yt-dlp` is on `PATH`, that video is
retried with yt-dlp and a notice is shown. `--youtube-backend builtin` turns
the fallback off; `--youtube-backend yt-dlp` uses yt-dlp for every video. yt-dlp
gets the same quality, audio-only, proxy, and speed-limit settings, plus any
YouTube/Google cookies from `--cookies` or `--cookies-from-browser`.

### Albums (Bunker, GoonBox, CyberDrop, Filester, Pixeldrain, Turbo)

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
| `--concurrent`   | `3`     | Maximum concurrent downloads                 |
| `--per-host`     | `2`     | Maximum concurrent downloads from one server (`0` = no limit) |
| `--config`       | (none)  | Config file path (default: `~/.scraper.yaml`)|
| `-i, --input-file`| (none) | Read URLs from a file, one per line (batch mode); an optional tab-separated album URL per line keeps retries in the album folder |
| `--limit-rate`   | (none)  | Cap total download speed (e.g. `500K`, `2M`, `1G`); unlimited by default |
| `--checksums`    | `false` | Write and verify `.sha256` checksum sidecar files (default: off) |
| `-n, --dry-run`  | `false` | List item count/total size without downloading anything |
| `--no-tui`       | `false` | Disable the terminal UI; print plain text output |
| `-j, --json`     | `false` | Output JSON Lines (one event per line); implies `--no-tui` |
| `--proxy`        | (none)  | HTTP/S proxy URL for all requests (e.g. `http://proxy:8080`) |
| `--cookies`      | (none)  | Netscape-format cookies file for HTTP requests |
| `--quality`      | (auto)  | Maximum YouTube quality (`2160p`, `1440p`, `1080p`, `720p`, `480p`, `360p`, `240p`) |
| `--audio-only`   | (off)   | YouTube audio only; optional format: `m4a` (default) or `mp3` |
| `--youtube-backend` | `auto` | YouTube downloader: `auto` (built-in, falls back to yt-dlp), `builtin`, or `yt-dlp` |
| `--include`      | (none)  | Glob pattern(s) to filter items (repeatable, e.g. `--include '*.mp4'`) |
| `--exclude`      | (none)  | Glob pattern(s) to exclude items                          |
| `--allow-ext`    | (none)  | Extra file extensions to keep with `--media-only` (repeatable, e.g. `--allow-ext cbz`) |
| `--max-album-items` | `10000` | Cap items contributed by one album (0 = unlimited) |
| `--print-urls`   | `false` | Print item URLs (one per line) and exit without downloading |
| `--scrape-delay` | (none)  | Pause between album scrapes (e.g. `500ms`) |
| `--log-file`     | (none)  | Append a timestamped run log (scrape errors, retry summaries) |
| `--watch`        | (none)  | Re-run the input list every interval until interrupted (implies `--no-tui`) |
| `--cookies-from-browser` | (none) | Import cookies for the supported sites from a local browser profile (`firefox`; macOS, Linux incl. snap/flatpak, Windows) |
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

See [Dashboard keys](#dashboard-keys) for navigation, cancellation, retries,
search, filtering, folding, and summary actions.

### Media-only downloads (default)

By default the scraper only queues media and archive files. Items whose
filename (or, when the title carries no extension, the URL) ends in a known
media or archive extension are kept:

- video: mp4, m4v, mkv, webm, avi, mov, wmv, flv, mpg, mpeg, ts, m2ts, 3gp, ogv
- audio: mp3, m4a, aac, flac, wav, ogg, opus, wma, m4b, aiff
- image: jpg, jpeg, png, gif, webp, bmp, avif, heic, heif, tiff, tif
- archives: zip, rar, 7z, tar, gz, tgz, bz2, xz, tbz2, txz, iso
- split-archive volumes: `.r00`–`.r99` (RAR), `.z01`–`.z99` (Zip),
  `.001`–`.999` (7-Zip / Zip parts) — so multi-volume sets download whole

Everything else — executables, scripts, HTML, PDFs, and unknown file types —
is skipped before download. A title with no extension is kept only when the
site's API reports a `video/`, `audio/`, or `image/` MIME type for it. When
anything is skipped, the run prints
`media filter: N/M items (K skipped non-media)`; YouTube is unaffected.

Pass `--media-only=false` (or set `media_only: false` in the config) to
download everything an album lists. To keep additional file types, pass
`--allow-ext` (repeatable) or list them under `media_extensions` in the
config:

    ./scraper --allow-ext cbz --allow-ext iso https://example.com/a/xyz

The allowlist itself is name/MIME based, but downloads get a light content
check: the first bytes are sniffed, and HTML or executables that claim to be
media (a payload renamed to `video.mp4`) are refused before they are kept.
Archives are not inspected, resumed downloads are not re-sniffed, and
non-media items requested with `--media-only=false` are left alone.

## Retrying failed downloads

When a run finishes with failures, the scraper writes a retry list to the
output directory: `scraper-failed-<date>-<time>.txt`, and prints a grouped
summary of why they failed, for example:

    Failed 91 item(s):
      86 × HTTP 503 from pc9.scdn.st
      5 × connection dropped
    Retry list: ~/Downloads/scraper-failed-20260926-060900.txt

Each line pairs the failed item's page URL with the album it came from,
tab-separated:

    https://bunkr.cr/f/5RWka6Gp2cjOv	https://bunkr.cr/a/rBSKhzaL

Re-running the list scrapes only those items but downloads them into their
original album folder, so retried files land next to the rest of the album:

    ./scraper --input-file ~/Downloads/scraper-failed-20260926-060900.txt

Many 503s from one host mean it is overloaded; retry with `--per-host 1`.

Single-URL lines still work (extension exports, hand-written lists) and
download to the URL's own folder; lines starting with `#` are comments.
Items that have no page of their own (goonbox's direct image links, for
example) are written as their album URL alone — replaying re-scrapes that
album and downloads only the missing items into it.

JSON mode adds the retry-list path to the final `summary` event as
`retryFile`; partial results set `partial: true`, and partial scrape
results also emit a `scrape_warning` event. Existing output files are reused
only when their source identity and content digest can be verified. Legacy or
unverified files are preserved, and a new download is saved under a
collision-safe name.
Transient failures are retried in-run with exponential backoff (5 attempts,
honoring `Retry-After` when the server sends it) and signed URLs are
re-resolved once when an attempt fails, so many blips recover on their own.

## Configuration

A YAML config file can be placed at `~/.scraper.yaml`:

```yaml
output_dir: ~/Downloads
concurrent_downloads: 10
per_host_concurrency: 2  # per server; 0 = no limit
timeout: 30s
user_agent: "Scraper/1.0"
rate_limit: 2M
checksums: false
proxy: http://proxy:8080
cookies: ~/.scraper-cookies.txt
quality: "1080p"
audio_only: ""  # off; or "m4a" / "mp3" — a YAML boolean here fails to load
youtube_backend: auto  # or "builtin" / "yt-dlp"
include: ["*.mp4", "*.mkv"]
exclude: ["*.txt"]
media_extensions: []
max_album_items: 10000
scrape_delay: 0s
log_file: ""
min_size: "1M"
max_size: "10G"
disk_check: true
media_only: true
notify: false
```

CLI flags override config file values. `--proxy`/`proxy` routes every
outbound request (scraping and downloads) through the given HTTP/S proxy.

Checksum sidecars are **off by default**: no `.sha256` files are written,
and existing files are skipped with a fast size check instead of being
re-hashed. Pass `--checksums` (or set `checksums: true`) to write a
`<file>.sha256` sidecar (standard `sha256sum`-compatible format) next to
each completed download; with checksums enabled, re-running a download
verifies the existing file and redownloads it if the sidecar no longer
matches (a corrupted file) instead of trusting it as-is.
`rate_limit`/`--limit-rate` caps the *combined* speed across all concurrent
downloads, not each one individually.

`per_host_concurrency`/`--per-host` caps how many of those downloads hit the
same server (CDN node) at once. When a server answers 429 or 503, every
download from it pauses for the `Retry-After` time (or the retry backoff)
instead of retrying in a burst. If a host such as a Bunkr CDN node still
returns many 503s, try `--per-host 1`.

Interrupted direct HTTP downloads retain validated `.part` files and resume
with Range requests. YouTube streams also resume, after a retry or in a later
run: partial streams wait in a hidden `.scraper-yt-*` folder beside the output
and are reused only if the video, format, size, and last-modified time still
match. The folder is removed once the video is saved. Delete leftover
`.scraper-yt-*` folders to discard partial YouTube downloads you no longer want.

## Supported Sites

| Site      | URL format                          | Notes                                |
|-----------|-------------------------------------|--------------------------------------|
| YouTube   | `youtu.be/ID`, `youtube.com/watch?v=ID`, or a `playlist?list=ID` URL | Highest available by default; `--quality` caps video resolution; auto-mux |
| CyberDrop | `cyberdrop.cr/f/SLUG` or `/a/SLUG` | API-based, signed CDN URLs           |
| Bunker    | `bunkr.cr/a/ALBUM_ID`              | API + token signing, parallel fetch  |
| GoonBox   | `goonbox.cr/a/ALBUM_ID`            | JSON API, paginated albums           |
| Filester  | `filester.sh/f/ID` or `/d/SLUG`    | HTML folder listing + signed download tokens, paginated |
| Pixeldrain| `pixeldrain.com/l/LIST_ID` or `/u/FILE_ID` | JSON API, direct download, Range-request resume |
| Turbo     | `turbo.cr/a/ALBUM_ID`, `/d/FILE_ID` or `/v/FILE_ID` | Album page listing, signed download URLs resolved at download time |

Pixeldrain throttles download speed after ~6 GB per IP in a 24h window. To bypass this, use `--proxy` to switch IPs and regain full speed. Range-request resume also reuses partial downloads across runs, so an interrupted file picks up where it left off.

## Development

```bash
go build ./...
go vet ./...
go test ./...
```

The GUI is a separate Go module in `gui/` (so the CLI stays CGO-free):

```bash
cd gui/frontend && npm ci && npm run check && npm test && npm run build
cd .. && go test ./...
wails generate module   # after changing the bound backend API
```

Shared code: `internal/app` (site detection, filters, previews, downloaders,
retry lists), `internal/session` (the download-run state machine both the TUI
dashboard and the GUI drive), `internal/config` (load/save).
