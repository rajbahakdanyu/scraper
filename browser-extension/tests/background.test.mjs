import test from "node:test";
import assert from "node:assert/strict";

import { waitForDownload, downloadText, exportGroups, exportTabs } from "../background.mjs";

class FakeDownloads {
  constructor({ autoComplete = false, failFilenames = new Set() } = {}) {
    this.listeners = new Set();
    this.items = new Map();
    this.downloads = [];
    this.nextId = 1;
    this.autoComplete = autoComplete;
    this.failFilenames = failFilenames;
  }

  get onChanged() {
    const self = this;
    return {
      addListener: (fn) => self.listeners.add(fn),
      removeListener: (fn) => self.listeners.delete(fn),
    };
  }

  async download(options) {
    if (this.failFilenames.has(options.filename)) {
      throw new Error(`simulated failure for ${options.filename}`);
    }
    const id = this.nextId++;
    this.downloads.push({ id, ...options });
    this.items.set(id, { id, state: "in_progress" });
    if (this.autoComplete) {
      queueMicrotask(() => this.emit(id, "complete"));
    }
    return id;
  }

  async search({ id }) {
    const item = this.items.get(id);
    return item ? [item] : [];
  }

  emit(id, state, error) {
    this.items.set(id, { id, state, error });
    for (const listener of [...this.listeners]) {
      listener({ id, state: { current: state }, error: error ? { current: error } : undefined });
    }
  }
}

const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

test("waitForDownload resolves on complete and removes its listener", async () => {
  const fake = new FakeDownloads();
  const pending = waitForDownload(fake, 1);
  fake.emit(1, "complete");
  await pending;
  assert.equal(fake.listeners.size, 0);
});

test("waitForDownload rejects with the interrupt reason", async () => {
  const fake = new FakeDownloads();
  const pending = waitForDownload(fake, 1);
  fake.emit(1, "interrupted", "NETWORK_FAILED");
  await assert.rejects(pending, /NETWORK_FAILED/);
  assert.equal(fake.listeners.size, 0);
});

test("waitForDownload catches downloads that already finished", async () => {
  const fake = new FakeDownloads();
  fake.items.set(1, { id: 1, state: "complete" });
  await waitForDownload(fake, 1);
});

test("downloadText keeps the blob alive until completion", async () => {
  const revoked = [];
  const original = URL.revokeObjectURL;
  URL.revokeObjectURL = (url) => revoked.push(url);
  try {
    const fake = new FakeDownloads();
    let resolved = false;
    const pending = downloadText(fake, "scraper-tabs.txt", "https://bunkr.cr/a/x\n").then(() => {
      resolved = true;
    });

    await tick();
    assert.equal(resolved, false, "must not resolve before the download completes");
    assert.equal(revoked.length, 0, "must not revoke before the download completes");
    assert.equal(fake.downloads.length, 1);
    assert.equal(fake.downloads[0].filename, "scraper-tabs.txt");
    assert.match(fake.downloads[0].url, /^blob:/);

    fake.emit(1, "complete");
    await pending;
    assert.equal(revoked.length, 1, "must revoke after completion");
  } finally {
    URL.revokeObjectURL = original;
  }
});

test("downloadText revokes when the download cannot start", async () => {
  const revoked = [];
  const original = URL.revokeObjectURL;
  URL.revokeObjectURL = (url) => revoked.push(url);
  try {
    const fake = new FakeDownloads({ failFilenames: new Set(["scraper-tabs.txt"]) });
    await assert.rejects(downloadText(fake, "scraper-tabs.txt", "https://x\n"), /simulated failure/);
    assert.equal(revoked.length, 1);
  } finally {
    URL.revokeObjectURL = original;
  }
});

test("exportGroups skips empty groups, keeps going after a failure, and reports results", async () => {
  const fake = new FakeDownloads({
    autoComplete: true,
    failFilenames: new Set(["scraper-tabs.txt"]),
  });
  const results = await exportGroups(fake, {
    other: ["https://bunkr.cr/a/x"],
    youtube: ["https://youtube.com/watch?v=1"],
  });
  assert.deepEqual(results, [
    { filename: "scraper-tabs.txt", count: 1, ok: false, error: "simulated failure for scraper-tabs.txt" },
    { filename: "scraper-youtube.txt", count: 1, ok: true },
  ]);
});

test("exportTabs queries tabs, groups them, and reports the skipped count", async () => {
  const fake = new FakeDownloads({ autoComplete: true });
  const tabsApi = {
    query: async () => [
      { url: "https://youtube.com/watch?v=1", windowId: 0, index: 0 },
      { url: "https://google.com/", windowId: 0, index: 1 },
    ],
  };
  const { skipped, results } = await exportTabs(tabsApi, fake);
  assert.equal(skipped, 1);
  assert.deepEqual(results, [{ filename: "scraper-youtube.txt", count: 1, ok: true }]);
});
