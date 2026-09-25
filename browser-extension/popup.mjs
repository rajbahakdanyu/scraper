const button = document.getElementById("export");
const status = document.getElementById("status");

function setStatus(text) {
  status.textContent = text;
}

button.addEventListener("click", async () => {
  button.disabled = true;
  setStatus("Exporting tabs…");
  try {
    // The background page does the querying and downloading so the export
    // continues even if this popup closes before it finishes.
    const { skipped, results } = await browser.runtime.sendMessage({ type: "export-tabs" });

    if (results.length === 0) {
      setStatus(`No supported tabs found (${skipped} skipped).`);
      return;
    }

    const ok = results.filter((result) => result.ok);
    const failed = results.filter((result) => !result.ok);
    let summary = ok.length > 0
      ? `Exported ${ok.map((result) => `${result.count} → ${result.filename}`).join(", ")}`
      : "Nothing exported";
    if (failed.length > 0) {
      summary += `; failed: ${failed.map((result) => `${result.filename} (${result.error})`).join(", ")}`;
    }
    if (skipped > 0) {
      summary += ` (${skipped} skipped)`;
    }
    setStatus(`${summary}.`);
  } catch (err) {
    setStatus(`Export failed: ${err?.message ?? err}`);
  } finally {
    button.disabled = false;
  }
});
