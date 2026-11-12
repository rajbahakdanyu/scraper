---
title: Batch downloads
description: Download many URLs, from any mix of sites, in one run.
---

Pass several URLs to download them all in one dashboard. Each is scraped in
the order given and downloads into its own album subfolder under the output
directory.

```bash
scraper https://youtu.be/VIDEO_ID "https://www.youtube.com/playlist?list=ID"
scraper https://cyberdrop.cr/f/FILE_ID https://bunkr.cr/a/ALBUM_ID
scraper https://youtu.be/VIDEO_ID https://bunkr.cr/a/ALBUM_ID
```

Sites can be mixed freely. YouTube links run first as their own group, then
everything else; the non-YouTube group may itself mix sites.

## From a file

Read URLs from a file, one per line, with `-i`/`--input-file`. Lines starting
with `#` are comments.

```bash
scraper -i urls.txt
```

A line may add a tab-separated album URL after the item URL so that the item
lands in that album's folder. This is the format of
[retry lists](/guides/retrying/).

## From stdin

When there are no URL arguments and no `--input-file`, URLs piped on stdin
are used:

```bash
cat urls.txt | scraper
```

## Watch mode

`--watch` re-runs the input list every interval until you interrupt it, and
implies `--no-tui`. Files that are already downloaded and verified are reused,
so later passes mostly fetch what's new:

```bash
scraper --watch 30m -i urls.txt
```

The [desktop app](/guides/desktop-app/#watching-in-the-background)
can keep watching with its window hidden.
