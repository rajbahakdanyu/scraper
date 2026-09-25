import { collectGroups, formatTxt } from "./collect.mjs";

const button = document.getElementById("export");
const status = document.getElementById("status");

function setStatus(text) {
  status.textContent = text;
}

async function downloadText(filename, text) {
  const blob = new Blob([text], { type: "text/plain" });
  const url = URL.createObjectURL(blob);
  try {
    await browser.downloads.download({ url, filename, saveAs: false });
  } finally {
    URL.revokeObjectURL(url);
  }
}

button.addEventListener("click", async () => {
  button.disabled = true;
  setStatus("Reading tabs…");
  try {
    const tabs = await browser.tabs.query({});
    const { youtube, other, skipped } = collectGroups(tabs);

    if (youtube.length === 0 && other.length === 0) {
      setStatus(`No supported tabs found (${skipped} skipped).`);
      return;
    }

    const exported = [];
    if (other.length > 0) {
      await downloadText("scraper-tabs.txt", formatTxt(other));
      exported.push(`${other.length} → scraper-tabs.txt`);
    }
    if (youtube.length > 0) {
      await downloadText("scraper-youtube.txt", formatTxt(youtube));
      exported.push(`${youtube.length} → scraper-youtube.txt`);
    }
    const skippedNote = skipped > 0 ? ` (${skipped} skipped)` : "";
    setStatus(`Exported ${exported.join(", ")}${skippedNote}.`);
  } catch (err) {
    setStatus(`Export failed: ${err?.message ?? err}`);
  } finally {
    button.disabled = false;
  }
});
