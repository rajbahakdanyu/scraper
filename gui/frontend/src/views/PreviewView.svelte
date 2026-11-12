<script lang="ts">
  import { CancelPreview, StartRun } from '../../bindings/github.com/rajbahakdanyu/scraper/gui/backend/app'
  import { humanBytes } from '../lib/format'
  import { errorText, resetRun, toast, ui } from '../lib/state.svelte'

  const albums = $derived(ui.preview)
  const totals = $derived.by(() => {
    let items = 0
    let bytes = 0
    let unknown = 0
    for (const a of albums ?? []) {
      items += a.items.length
      bytes += a.totalSize
      unknown += a.unknown
    }
    return { items, bytes, unknown }
  })

  async function back() {
    await CancelPreview()
    ui.view = 'home'
  }

  async function download() {
    try {
      resetRun()
      await StartRun({ ...ui.input })
      ui.view = 'run'
    } catch (err) {
      toast(errorText(err))
    }
  }

  function summary(n: number, bytes: number, unknown: number) {
    const base = `${n} item${n === 1 ? '' : 's'}, ${humanBytes(bytes)} total`
    return unknown > 0 ? `${base} (${unknown} with unknown size)` : base
  }
</script>

<section class="preview">
  <div class="row">
    <h2 class="grow">Preview <span class="dim small">(nothing is downloaded)</span></h2>
    <button onclick={back}>Back</button>
    <button class="primary" disabled={!albums || totals.items === 0 || !!ui.run?.running} onclick={download}>Download</button>
  </div>

  {#if !albums}
    <div class="panel pad"><span class="spin">◆</span> Scraping…</div>
  {:else}
    <p class="mono">{summary(totals.items, totals.bytes, totals.unknown)}</p>
    {#each albums as a (a.url)}
      <div class="panel album">
        <div class="row">
          <span class="site">{a.site || '?'}</span>
          <span class="mono ellipsis grow" title={a.url}>{a.url}</span>
          {#if a.slug}<span class="dim">→ {a.slug}/</span>{/if}
        </div>
        {#if a.error}
          <p class="err">✗ {a.error}</p>
        {:else if a.items.length === 0}
          <p class="dim">No items found.</p>
        {:else}
          {#if a.warning}<p class="warn small">Warning: {a.warning}</p>{/if}
          {#if a.diskWarning}<p class="warn small">{a.diskWarning}</p>{/if}
          <table class="mono small">
            <tbody>
              {#each a.items as it, i (i)}
                <tr><td class="ellipsis">{it.title}</td><td class="size">{it.size > 0 ? humanBytes(it.size) : 'unknown size'}</td></tr>
              {/each}
            </tbody>
          </table>
          <p class="dim small">{summary(a.items.length, a.totalSize, a.unknown)}</p>
        {/if}
      </div>
    {/each}
  {/if}
</section>

<style>
  .preview {
    max-width: 1000px;
    margin: 0 auto;
    padding: 18px;
    display: flex;
    flex-direction: column;
    gap: 10px;
  }
  h2 {
    margin: 0;
    font-size: 18px;
  }
  .small {
    font-size: 12px;
  }
  .pad {
    padding: 20px;
  }
  .album {
    padding: 10px 12px;
  }
  .site {
    font-size: 11px;
    border-radius: 4px;
    padding: 1px 6px;
    background: var(--accent-dim);
  }
  table {
    width: 100%;
    border-collapse: collapse;
    table-layout: fixed;
    margin-top: 6px;
    user-select: text;
  }
  td {
    padding: 2px 4px;
    border-bottom: 1px solid rgba(255, 255, 255, 0.04);
  }
  .size {
    width: 120px;
    text-align: right;
    color: var(--dim);
  }
  p {
    margin: 6px 0 0;
  }
  .spin {
    color: var(--accent);
  }
</style>
