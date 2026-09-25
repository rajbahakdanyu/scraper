import { collectGroups, formatTxt } from "./collect.mjs";

// How long to wait for downloads.onChanged before giving up on a download.
const DOWNLOAD_TIMEOUT_MS = 15000;

// waitForDownload resolves when the download reached "complete" and rejects
// when it was "interrupted". Per MDN, a blob URL must not be revoked until
// the download completes, and the promise from downloads.download() only
// means the download started.
export function waitForDownload(downloads, id, timeoutMs = DOWNLOAD_TIMEOUT_MS) {
  return new Promise((resolve, reject) => {
    let settled = false;
    const listener = (delta) => {
      if (delta.id !== id || !delta.state) return;
      if (delta.state.current === "complete") {
        finish(resolve);
      } else if (delta.state.current === "interrupted") {
        finish(reject, new Error(`download interrupted: ${delta.error?.current ?? "unknown"}`));
      }
    };
    const finish = (fn, arg) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      downloads.onChanged.removeListener(listener);
      fn(arg);
    };
    const timer = setTimeout(() => finish(reject, new Error("timed out waiting for download")), timeoutMs);
    downloads.onChanged.addListener(listener);

    // The download may have finished before the listener was registered;
    // search once to close that race.
    downloads
      .search({ id })
      .then((items) => {
        const item = items?.[0];
        if (item?.state === "complete") finish(resolve);
        else if (item?.state === "interrupted") {
          finish(reject, new Error(`download interrupted: ${item.error ?? "unknown"}`));
        }
      })
      .catch(() => {});
  });
}

// downloadText writes text to Downloads and only revokes the blob URL after
// the download has actually completed. The blob lives in the caller's
// document, so the caller (the background page) must stay alive until this
// resolves.
export async function downloadText(downloads, filename, text) {
  const blobUrl = URL.createObjectURL(new Blob([text], { type: "text/plain" }));
  let id;
  try {
    id = await downloads.download({ url: blobUrl, filename, saveAs: false });
  } catch (err) {
    URL.revokeObjectURL(blobUrl);
    throw err;
  }
  try {
    await waitForDownload(downloads, id);
  } finally {
    URL.revokeObjectURL(blobUrl);
  }
  return filename;
}

// exportGroups downloads each non-empty group, continuing to the next file
// when one fails so a single bad download cannot block the other export.
export async function exportGroups(downloads, groups) {
  const results = [];
  const runs = [
    ["scraper-tabs.txt", groups.other],
    ["scraper-youtube.txt", groups.youtube],
  ];
  for (const [filename, urls] of runs) {
    if (!urls?.length) continue;
    try {
      await downloadText(downloads, filename, formatTxt(urls));
      results.push({ filename, count: urls.length, ok: true });
    } catch (err) {
      results.push({ filename, count: urls.length, ok: false, error: err?.message ?? String(err) });
    }
  }
  return results;
}

// exportTabs is the whole background-side flow: read every tab, group the
// supported ones, download the files, report what happened.
export async function exportTabs(tabsApi, downloadsApi) {
  const tabs = await tabsApi.query({});
  const groups = collectGroups(tabs);
  const results = await exportGroups(downloadsApi, groups);
  return { skipped: groups.skipped, results };
}

// Runtime wiring. Guarded so the module can be imported by Node tests.
if (typeof browser !== "undefined") {
  browser.runtime.onMessage.addListener((message) => {
    if (message?.type !== "export-tabs") return undefined;
    return exportTabs(browser.tabs, browser.downloads);
  });
}
