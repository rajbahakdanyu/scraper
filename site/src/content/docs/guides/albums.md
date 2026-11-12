---
title: Albums and files
description: How album and single-file links from file hosts are downloaded.
---

For Bunkr, GoonBox, CyberDrop, Filester, Pixeldrain, and Turbo, every file
in an album or folder downloads concurrently into a subfolder named after the
album ID:

```
~/Downloads/
  k4OuX4pJ/        ← album subfolder
    image1.png
    video1.mp4
```

Single-file links (such as CyberDrop `/f/` or Filester `/d/`) download
straight into the output directory. In a [batch](/scraper/guides/batch-downloads/)
each URL gets its own subfolder, the same as albums.

## Concurrency and politeness

| Setting | Default | Effect |
| --- | --- | --- |
| `--concurrent` | `3` | Downloads running at once |
| `--per-host` | `2` | Downloads hitting one server (CDN node) at once; `0` = no limit |
| `--limit-rate` | unlimited | Combined speed across all downloads, e.g. `2M` |
| `--scrape-delay` | none | Pause between album scrapes, e.g. `500ms` |
| `--max-album-items` | `10000` | Cap items from one album; `0` = unlimited |

When a server answers 429 or 503, every download from it pauses for the
`Retry-After` time (or the retry backoff) instead of retrying in a burst. If a
host such as a Bunkr CDN node still returns many 503s, try `--per-host 1`.

## Resuming and existing files

Interrupted downloads keep validated `.part` files and resume with HTTP Range
requests, including across runs.

Existing output files are reused only when their source and content can be
verified. Unverified files are left alone, and the new download is saved
under a non-clashing name.

With `--checksums`, a `sha256sum`-compatible `<file>.sha256` sidecar is
written next to each download. Re-running then verifies existing files and
re-downloads any that no longer match. Without it (the default), existing
files are checked by size only.

## Pixeldrain limits

Pixeldrain throttles speed after about 6 GB per IP in 24 hours. Use `--proxy`
to switch IPs; resumed downloads pick up where they left off.
