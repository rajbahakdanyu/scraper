---
title: Browser extension
description: Export your open Firefox tabs as URL lists for Scraper.
---

**Scraper Tab Export** is a Firefox extension that saves every open tab on a
supported site as an [`--input-file`](/scraper/guides/batch-downloads/#from-a-file)
list in your Downloads folder.

## Install

1. Download `scraper-tab-export-<version>.xpi` from the
   [latest release](https://github.com/rajbahakdanyu/scraper/releases/latest),
   or use `browser-extension/manifest.json` from a clone.
2. Open `about:debugging#/runtime/this-firefox`.
3. Click **Load Temporary Add-on…** and select the file.

The add-on stays loaded until Firefox restarts.

## Use

1. Open your album and file tabs, in any windows.
2. Click the extension button, then **Export supported tabs**. The export runs
   in the background, so you can close the popup.
3. These files appear in your Downloads folder:

   | File | Contents |
   | --- | --- |
   | `scraper-tabs.txt` | Supported non-YouTube tabs |
   | `scraper-youtube.txt` | YouTube tabs |

   A file is only created when its group has tabs. Unsupported tabs are
   counted and skipped.
4. Run them:

   ```bash
   scraper --dry-run --input-file ~/Downloads/scraper-tabs.txt
   scraper --input-file ~/Downloads/scraper-tabs.txt
   scraper --input-file ~/Downloads/scraper-youtube.txt
   ```

Tabs in private windows are only exported if you allow the extension in
private browsing.
