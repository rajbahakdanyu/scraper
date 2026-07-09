# Usage Guide

## Commands

### `scraper youtube <url>`

Scrapes video metadata via YouTube's oEmbed endpoint and downloads the
resulting item(s).

### `scraper gofile <url>`

Calls the GoFile content API (`https://api.gofile.io/contents/{id}`) and
downloads every child file returned.

### `scraper cyberdrop <url>`

Fetches the given CyberDrop page directly and extracts `a.image-link`
elements.

### `scraper bunker <url>`

Fetches the given Bunker page directly and extracts `a.link` elements.

### `scraper tui <site> <url>`

`<site>` is one of `youtube`, `gofile`, `cyberdrop`, `bunker`. Parses the URL
with the matching scraper, then opens a full-screen dashboard to review and
download items interactively. See the [README](../README.md#interactive-tui)
for keybindings.

## Flags Reference

All of `youtube`, `gofile`, `cyberdrop`, and `bunker` accept:

- `-u, --url string` — content URL, an alternative to the positional argument.
- `-o, --output string` — output directory (default `.`).
- `--concurrent int` — maximum number of simultaneous downloads (default `5`).

`tui` accepts `-o/--output` and `--concurrent` (no `--url`, since the URL is
a required positional argument alongside the site name).

## Exit Behavior

Each direct-download subcommand blocks until every item has finished
downloading (successfully or not), then exits non-zero if any download
failed — printing a `done: <title>` or `failed: <title> (<error>)` line per
item along the way.

## Config File

`internal/config.Load(path)` reads a YAML file shaped like:

```yaml
output_dir: ~/Downloads
concurrent_downloads: 10
timeout: 30s
user_agent: "Scraper/1.0"
```

Defaults are applied for zero-valued fields (`concurrent_downloads: 5`,
`timeout: 30s`). This loader is not yet invoked by the CLI commands — it's
available for programmatic use and future `--config` flag wiring.
