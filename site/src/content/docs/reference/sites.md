---
title: Supported sites
description: URL formats Scraper recognizes and how each site is fetched.
---

The site is detected from the URL's host. You can also force it with a
subcommand (`youtube`, `cyberdrop`, `bunker`, `goonbox`, `filester`,
`pixeldrain`, `turbo`).

| Site | URL formats | How it's fetched |
| --- | --- | --- |
| YouTube | `youtu.be/ID`, `youtube.com/watch?v=ID`, `playlist?list=ID` | Highest quality by default; `--quality` caps resolution; auto-mux. See [YouTube](/guides/youtube/) |
| CyberDrop | `cyberdrop.cr/f/SLUG`, `/a/SLUG` | API with signed CDN URLs |
| Bunkr | `bunkr.cr/a/ALBUM_ID` | API with token signing, parallel fetch |
| GoonBox | `goonbox.cr/a/ALBUM_ID` | JSON API, paginated albums |
| Filester | `filester.sh/f/ID`, `/d/SLUG` | HTML folder listing with signed download tokens, paginated |
| Pixeldrain | `pixeldrain.com/l/LIST_ID`, `/u/FILE_ID` | JSON API, direct download, Range resume |
| Turbo | `turbo.cr/a/ALBUM_ID`, `/d/FILE_ID`, `/v/FILE_ID` | Album page listing; signed URLs resolved at download time |

Pixeldrain throttles speed after about 6 GB per IP in 24 hours; see
[Albums and files](/guides/albums/#pixeldrain-limits).
