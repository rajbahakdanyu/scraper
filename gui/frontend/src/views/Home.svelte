<script lang="ts">
  import { onMount } from 'svelte'
  import {
      ApplySettings,
      ChooseOutputDir,
      DetectSites,
      GetOptions,
      GetSettings,
      ImportURLList,
      Preview,
      StartRun,
      StartWatch,
  } from '../../wailsjs/go/backend/App'
  import type { backend } from '../../wailsjs/go/models'
  import { errorText, resetRun, toast, ui } from '../lib/state.svelte'

  let detected = $state<backend.DetectedURL[]>([])
  let sites = $state<string[]>([])
  let outputDir = $state('')
  let watchEvery = $state('30m')
  let busy = $state('')
  let error = $state('')
  let detectTimer: ReturnType<typeof setTimeout> | undefined

  const single = $derived(detected.length === 1 && !ui.input.fromFile)
  const runnable = $derived(detected.length > 0 && (ui.input.site !== '' ? single : detected.every((d) => d.site)))

  onMount(async () => {
    try {
      sites = (await GetOptions()).sites
      outputDir = (await GetSettings()).outputDir
      await detect()
    } catch (err) {
      error = errorText(err)
    }
  })

  $effect(() => {
    if (!single && ui.input.site) ui.input.site = ''
  })

  function onInput() {
    clearTimeout(detectTimer)
    detectTimer = setTimeout(detect, 200)
  }

  async function detect() {
    try {
      detected = (await DetectSites(ui.input.text)) ?? []
    } catch (err) {
      error = errorText(err)
    }
  }

  async function importList() {
    try {
      const text = await ImportURLList()
      if (!text) return
      ui.input.text = text
      ui.input.fromFile = true
      await detect()
    } catch (err) {
      error = errorText(err)
    }
  }

  async function chooseOutput() {
    try {
      const dir = await ChooseOutputDir()
      if (!dir) return
      const s = await GetSettings()
      s.outputDir = dir
      await ApplySettings(s)
      outputDir = dir
    } catch (err) {
      error = errorText(err)
    }
  }

  async function act(label: string, fn: () => Promise<void>) {
    error = ''
    busy = label
    try {
      await fn()
    } catch (err) {
      error = errorText(err)
    } finally {
      busy = ''
    }
  }

  const input = () => ({ text: ui.input.text, fromFile: ui.input.fromFile, site: ui.input.site })

  const start = () =>
    act('start', async () => {
      resetRun()
      await StartRun(input())
      ui.view = 'run'
    })

  const preview = () =>
    act('preview', async () => {
      ui.preview = null
      ui.view = 'preview'
      try {
        ui.preview = (await Preview(input())) ?? []
      } catch (err) {
        ui.view = 'home'
        throw err
      }
    })

  const watch = () =>
    act('watch', async () => {
      resetRun()
      await StartWatch(input(), watchEvery)
      toast(`Watching every ${watchEvery}`)
      ui.view = 'run'
    })
</script>

<section class="home">
  <div class="row">
    <h2 class="grow">Download</h2>
    <span class="dim">Save to</span>
    <span class="mono ellipsis out" title={outputDir}>{outputDir}</span>
    <button onclick={chooseOutput}>Change…</button>
  </div>

  <textarea
    bind:value={ui.input.text}
    oninput={onInput}
    rows="9"
    spellcheck="false"
    placeholder="Paste one URL per line (album, file, playlist, video).&#10;Retry lists (item URL ⇥ album URL) and # comments work too."
  ></textarea>

  <div class="row wrap">
    <button onclick={importList}>Import list…</button>
    {#if ui.input.fromFile}
      <span class="chip">imported list — runs as a batch <button class="link" onclick={() => (ui.input.fromFile = false)}>✕</button></span>
    {/if}
    <span class="grow"></span>
    <label class="row" title="Force the site for a single URL, like the per-site CLI subcommands">
      <span class="dim">Site</span>
      <select bind:value={ui.input.site} disabled={!single}>
        <option value="">Auto-detect</option>
        {#each sites as s (s)}<option value={s}>{s}</option>{/each}
      </select>
    </label>
  </div>

  {#if detected.length > 0}
    <ul class="detected panel">
      {#each detected as d, i (i)}
        <li class="row">
          {#if d.site || ui.input.site}
            <span class="site">{ui.input.site || d.site}</span>
          {:else}
            <span class="site bad">unsupported</span>
          {/if}
          <span class="mono ellipsis grow" title={d.url}>{d.url}</span>
          {#if d.albumUrl}<span class="dim ellipsis" title={d.albumUrl}>→ {d.albumUrl}</span>{/if}
        </li>
      {/each}
    </ul>
    <p class="dim small">
      {detected.length} URL{detected.length === 1 ? '' : 's'} ·
      {single ? 'single download' : 'batch (YouTube runs first, then other sites)'}
    </p>
  {/if}

  {#if error}<p class="err">{error}</p>{/if}

  <div class="row actions">
    <button class="primary" disabled={!runnable || !!busy || !!ui.run?.running} onclick={start}>
      {busy === 'start' ? 'Starting…' : 'Download'}
    </button>
    <button disabled={!runnable || !!busy} onclick={preview} title="List items and sizes without downloading (--dry-run)">Preview</button>
    <span class="grow"></span>
    <label class="row" title="Re-run the list every interval (--watch)">
      <span class="dim">Watch every</span>
      <input type="text" bind:value={watchEvery} size="6" />
    </label>
    <button disabled={!runnable || !!busy || !!ui.run?.running} onclick={watch}>Watch</button>
  </div>
  {#if ui.run?.running}
    <p class="dim small">A run is in progress — <button class="link" onclick={() => (ui.view = 'run')}>view it</button>.</p>
  {/if}
</section>

<style>
  .home {
    max-width: 960px;
    margin: 0 auto;
    padding: 18px;
    display: flex;
    flex-direction: column;
    gap: 12px;
  }
  h2 {
    margin: 0;
    font-size: 18px;
  }
  .out {
    max-width: 380px;
  }
  textarea {
    width: 100%;
    resize: vertical;
    font-family: var(--mono);
    font-size: 13px;
  }
  .wrap {
    flex-wrap: wrap;
  }
  .chip {
    border: 1px solid var(--accent-dim);
    border-radius: 12px;
    padding: 2px 10px;
    font-size: 12px;
  }
  .detected {
    list-style: none;
    margin: 0;
    padding: 6px 10px;
    max-height: 220px;
    overflow: auto;
  }
  .detected li {
    padding: 2px 0;
    font-size: 12px;
  }
  .site {
    min-width: 84px;
    text-align: center;
    font-size: 11px;
    border-radius: 4px;
    padding: 1px 6px;
    background: var(--accent-dim);
  }
  .site.bad {
    background: #5c1d1d;
  }
  .small {
    font-size: 12px;
    margin: 0;
  }
  .actions {
    margin-top: 4px;
  }
</style>
