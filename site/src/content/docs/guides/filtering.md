---
title: Filtering
description: Choose which files in an album are downloaded.
---

## Media-only (default)

By default only media and archive files are queued. An item is kept when its
filename (or, if the title has no extension, its URL) ends in a known
extension:

- **Video:** mp4, m4v, mkv, webm, avi, mov, wmv, flv, mpg, mpeg, ts, m2ts, 3gp, ogv
- **Audio:** mp3, m4a, aac, flac, wav, ogg, opus, wma, m4b, aiff
- **Image:** jpg, jpeg, png, gif, webp, bmp, avif, heic, heif, tiff, tif
- **Archives:** zip, rar, 7z, tar, gz, tgz, bz2, xz, tbz2, txz, iso
- **Split-archive volumes:** `.r00`–`.r99` (RAR), `.z01`–`.z99` (Zip),
  `.001`–`.999` (7-Zip and Zip parts), so multi-volume sets download whole

Executables, scripts, HTML, PDFs, and unknown types are skipped before
download. A title with no extension is kept only when the site reports a
`video/`, `audio/`, or `image/` MIME type. When anything is skipped, the run
prints `media filter: N/M items (K skipped non-media)`. YouTube is not
affected.

To keep extra types, pass `--allow-ext` (repeatable) or list them under
`media_extensions` in the [config](/scraper/reference/configuration/):

```bash
scraper --allow-ext cbz --allow-ext epub https://bunkr.cr/a/ALBUM_ID
```

To download everything an album lists, pass `--media-only=false` (or set
`media_only: false`).

### Content check

Downloads also get a light content check: the first bytes are sniffed, and
HTML or executables pretending to be media (say, a payload renamed to
`video.mp4`) are refused before they are kept. Archives aren't inspected,
resumed downloads aren't re-sniffed, and non-media items requested with
`--media-only=false` are left alone.

## Name patterns

`--include` and `--exclude` take glob patterns and can be repeated:

```bash
scraper --include '*.mp4' --include '*.mkv' https://bunkr.cr/a/ALBUM_ID
scraper --exclude '*.gif' https://bunkr.cr/a/ALBUM_ID
```

## Size

```bash
scraper --min-size 1M --max-size 2G https://pixeldrain.com/l/LIST_ID
```

Sizes accept suffixes such as `K`, `M`, and `G`.

## Disk space

Free space is checked before downloading. Turn it off with
`--disk-check=false`.
