---
title: Quick start
description: Download your first album or video.
---

Pass a URL. The site is detected from the host, and the terminal dashboard
opens immediately while the album is scraped in the background:

```bash
scraper https://youtu.be/VIDEO_ID
scraper https://filester.sh/f/FOLDER_ID
scraper https://pixeldrain.com/l/CSbjLYKs
```

Files go to `~/Downloads` by default; albums get their own subfolder. Change
it with `-o`:

```bash
scraper -o ~/Videos https://bunkr.cr/a/ALBUM_ID
```

## Preview first

`--dry-run` lists the item count and total size without downloading or
opening the dashboard:

```bash
scraper --dry-run https://filester.sh/f/FOLDER_ID
```

## Force a site

If detection ever guesses wrong, or for scripts, name the site as a
subcommand:

```bash
scraper youtube https://youtu.be/VIDEO_ID
scraper cyberdrop https://cyberdrop.cr/f/FILE_ID
scraper bunker https://bunkr.cr/a/ALBUM_ID
scraper goonbox https://goonbox.cr/a/ALBUM_ID
scraper filester https://filester.sh/d/FILE_SLUG
scraper pixeldrain https://pixeldrain.com/u/qZz2NiiW
scraper turbo https://turbo.cr/v/FILE_ID
```

## Next steps

- Download many URLs in one run with [batch mode](/guides/batch-downloads/).
- Learn the [dashboard keys](/guides/dashboard/).
- Save your defaults in [`~/.scraper.yaml`](/reference/configuration/).
- Prefer a window? Use the [desktop app](/guides/desktop-app/).
