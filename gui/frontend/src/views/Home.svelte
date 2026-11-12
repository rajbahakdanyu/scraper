<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
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
  const watchUnits = [
    { value: 's', label: 'seconds', one: 'second' },
    { value: 'm', label: 'minutes', one: 'minute' },
    { value: 'h', label: 'hours', one: 'hour' },
    { value: 'd', label: 'days', one: 'day' },
  ]
  let watchAmount = $state<number | null>(30)
  let watchUnit = $state('m')
  const watchValid = $derived(Number.isInteger(watchAmount) && (watchAmount ?? 0) >= 1)
  // Go durations have no day unit, so days are sent as hours.
  const watchInterval = $derived(watchUnit === 'd' ? `${(watchAmount ?? 0) * 24}h` : `${watchAmount}${watchUnit}`)
  const watchLabel = $derived.by(() => {
    const u = watchUnits.find((x) => x.value === watchUnit)!
    return `${watchAmount} ${watchAmount === 1 ? u.one : u.label}`
  })
  let busy = $state('')
  let detecting = $state(false)
  let error = $state('')
  let detectTimer: ReturnType<typeof setTimeout> | undefined
  let detectRequest = 0
  let userEdited = false

  const single = $derived(detected.length === 1 && !ui.input.fromFile)
  const batch = $derived(ui.input.fromFile || (!detecting && detected.length > 1))
  const hasInput = $derived(ui.input.text.trim().length > 0)
  const runnable = $derived(
    !detecting &&
      detected.length > 0 &&
      (ui.input.site !== '' ? single : detected.every((d) => d.site)),
  )

  function backendError(err: unknown): string {
    const detail = errorText(err)
    return 'go' in window
      ? detail
      : `Desktop backend unavailable. Open the app, or run "wails dev" in gui/ and use http://localhost:34115. (${detail})`
  }

  onMount(async () => {
    const request = detectRequest
    try {
      sites = (await GetOptions()).sites
      outputDir = (await GetSettings()).outputDir
      if (request === detectRequest && !userEdited && hasInput) void detect()
    } catch (err) {
      error = backendError(err)
    }
  })

  onDestroy(() => {
    clearTimeout(detectTimer)
    detectRequest++
  })

  $effect(() => {
    if (batch && ui.input.site) ui.input.site = ''
  })

  function onInput() {
    userEdited = true
    clearTimeout(detectTimer)
    ui.input.fromFile = false
    error = ''
    detected = []
    const request = ++detectRequest
    if (!hasInput) {
      detecting = false
      return
    }
    detecting = true
    detectTimer = setTimeout(() => void detect(request), 250)
  }

  async function detect(request = ++detectRequest) {
    if (!hasInput) {
      detected = []
      detecting = false
      return
    }
    detected = []
    detecting = true
    error = ''
    try {
      const result = await DetectSites(ui.input.text)
      if (request === detectRequest) detected = result ?? []
    } catch (err) {
      if (request === detectRequest) error = backendError(err)
    } finally {
      if (request === detectRequest) detecting = false
    }
  }

  function clearInput() {
    clearTimeout(detectTimer)
    detectRequest++
    detecting = false
    detected = []
    error = ''
    ui.input.text = ''
    ui.input.site = ''
    ui.input.fromFile = false
  }

  async function importList() {
    try {
      const text = await ImportURLList()
      if (!text) return
      userEdited = true
      ui.input.text = text
      ui.input.fromFile = true
      await detect()
    } catch (err) {
      error = backendError(err)
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
      error = backendError(err)
    }
  }

  async function act(label: string, fn: () => Promise<void>) {
    error = ''
    busy = label
    try {
      await fn()
    } catch (err) {
      error = backendError(err)
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
      await StartWatch(input(), watchInterval)
      toast(`Watching every ${watchLabel}`)
      ui.view = 'run'
    })
</script>

<section class="home">
  <div class="intro">
    <p class="eyebrow">DOWNLOAD MEDIA</p>
    <h1>What would you like to download?</h1>
    <p class="dim">Paste links from a supported site. Scraper will find the media and organize it for you.</p>
  </div>

  <div class="input-card panel">
    <div class="input-heading row">
      <label for="url-input">Links</label>
      {#if ui.input.fromFile}
        <span class="chip">
          Imported list · batch
          {#if !detecting && detected.length === 1}
            <button class="link" aria-label="Use imported link as a single download" onclick={() => (ui.input.fromFile = false)}>Use as single</button>
          {/if}
        </span>
      {/if}
      <span class="grow"></span>
      {#if hasInput}
        <button class="subtle" onclick={clearInput} aria-label="Clear links">Clear</button>
      {/if}
      <button class="subtle" onclick={importList}>Import list…</button>
    </div>

    <textarea
      id="url-input"
      bind:value={ui.input.text}
      oninput={onInput}
      rows="8"
      spellcheck="false"
      aria-describedby="url-help"
      placeholder="https://youtube.com/watch?v=…&#10;https://pixeldrain.com/u/…"
    ></textarea>
    <div class="input-footer row">
      <span id="url-help" class="dim small">One URL per line. You can also paste an album, playlist, or retry list.</span>
      <span class="grow"></span>
      <label class="row site-select">
        <span class="dim">Site</span>
        <select bind:value={ui.input.site} disabled={sites.length === 0 || batch} aria-label="Site" aria-describedby={batch ? 'site-help' : undefined}>
          <option value="">Auto-detect</option>
          {#each sites as s (s)}<option value={s}>{s}</option>{/each}
        </select>
      </label>
    </div>
    {#if batch}
      <p id="site-help" class="dim small site-help">Site selection is available for one link. Batches use auto-detect.</p>
    {/if}
  </div>

  {#if detecting}
    <div class="detection panel dim" role="status"><span class="spin">◆</span> Checking links…</div>
  {:else if detected.length > 0}
    <div class="detection-summary row">
      <strong>{detected.length} link{detected.length === 1 ? '' : 's'}</strong>
      <span class="dim">·</span>
      {#if ui.input.site}
        <span class="dim">Using {ui.input.site}</span>
      {:else}
        {@const supported = detected.filter((d) => d.site).length}
        <span class:warn={supported < detected.length} class:ok={supported === detected.length}>
          {supported} supported{supported < detected.length ? ` · ${detected.length - supported} unsupported` : ''}
        </span>
      {/if}
      <span class="grow"></span>
      <span class="dim small">{single ? 'Single download' : 'Batch · YouTube runs first'}</span>
    </div>
    <ul class="detected panel" aria-label="Detected links">
      {#each detected as d, i (i)}
        <li class="row">
          {#if d.site || ui.input.site}
            <span class="site">{ui.input.site || d.site}</span>
          {:else}
            <span class="site bad">{batch ? 'Unsupported' : 'Choose site'}</span>
          {/if}
          <span class="mono ellipsis grow" title={d.url}>{d.url}</span>
          {#if d.albumUrl}<span class="dim ellipsis album-url" title={d.albumUrl}>Album: {d.albumUrl}</span>{/if}
        </li>
      {/each}
    </ul>
  {:else if hasInput && !error}
    <div class="detection panel warn" role="status">No supported links detected. Check the URL or choose a site for a single link.</div>
  {:else}
    <div class="empty-hint">
      <span class="hint-icon" aria-hidden="true">↳</span>
      <span><strong>Start with a link</strong><br /><span class="dim">Paste it above or import a saved list.</span></span>
    </div>
  {/if}

  {#if error}<p class="err error-message" role="alert">{error}</p>{/if}

  <div class="destination panel row">
    <span class="folder-icon" aria-hidden="true">▰</span>
    <span class="destination-label"><strong>Save location</strong><br /><span class="mono dim ellipsis out" title={outputDir}>{outputDir || 'Loading…'}</span></span>
    <span class="grow"></span>
    <button class="subtle" onclick={chooseOutput}>Change folder…</button>
  </div>

  <div class="actions">
    <div class="action-main row">
      <button class="primary download-button" disabled={!runnable || !!busy || !!ui.run?.running} onclick={start}>
        {busy === 'start' ? 'Starting…' : '↓  Download'}
      </button>
      <button disabled={!runnable || !!busy} onclick={preview}>Preview</button>
      <span class="grow"></span>
      <div class="row watch-label" role="group" aria-label="Repeat interval" title="Re-run the list at this interval">
        <span class="dim">Repeat every</span>
        <input
          type="number"
          class="watch-amount"
          min="1"
          step="1"
          bind:value={watchAmount}
          aria-label="Repeat amount"
          aria-invalid={!watchValid}
        />
        <select bind:value={watchUnit} aria-label="Repeat unit">
          {#each watchUnits as u (u.value)}<option value={u.value}>{u.label}</option>{/each}
        </select>
      </div>
      <button disabled={!runnable || !watchValid || !!busy || !!ui.run?.running} onclick={watch}>Watch</button>
    </div>
    {#if !runnable && hasInput && !detecting && !error}
      <p class="dim small action-hint">
        {detected.length === 0
          ? 'Enter a supported URL to enable download options.'
          : batch
            ? 'Remove unsupported links or enter one link to choose its site.'
            : 'Choose a site for this link to continue.'}
      </p>
    {:else if !runnable && !hasInput}
      <p class="dim small action-hint">Download options appear after you add a supported link.</p>
    {/if}
  </div>

  {#if ui.run?.running}
    <p class="running-note dim small" role="status">A run is already in progress — <button class="link" onclick={() => (ui.view = 'run')}>view it</button>.</p>
  {/if}
</section>

<style>
  .home {
    max-width: 860px;
    margin: 0 auto;
    padding: clamp(20px, 5vh, 48px) 24px 32px;
    display: flex;
    flex-direction: column;
    gap: 16px;
  }
  .intro {
    margin-bottom: 4px;
  }
  .eyebrow {
    color: var(--accent);
    font-size: 11px;
    font-weight: 700;
    letter-spacing: 0.12em;
    margin: 0 0 8px;
  }
  h1 {
    font-size: clamp(22px, 3vw, 29px);
    letter-spacing: -0.025em;
    line-height: 1.2;
    margin: 0 0 8px;
  }
  .intro > p:last-child {
    margin: 0;
  }
  .out {
    display: inline-block;
    max-width: min(460px, 52vw);
    vertical-align: bottom;
  }
  .input-card {
    padding: 12px;
    box-shadow: 0 12px 40px rgba(0, 0, 0, 0.16);
  }
  .input-heading {
    margin-bottom: 10px;
    flex-wrap: wrap;
  }
  .input-heading label {
    font-weight: 650;
  }
  .subtle {
    background: transparent;
  }
  .input-card textarea {
    width: 100%;
    min-height: 150px;
    resize: vertical;
    border-color: transparent;
    background: var(--bg);
    font-family: var(--mono);
    font-size: 13px;
    line-height: 1.7;
    padding: 12px;
  }
  textarea::placeholder {
    color: #687482;
  }
  .input-card textarea:focus {
    outline: 1px solid var(--accent-dim);
    border-color: var(--accent-dim);
  }
  .input-footer {
    padding: 10px 2px 0;
  }
  .site-select {
    font-size: 12px;
  }
  .site-select select {
    padding: 4px 8px;
  }
  .chip {
    border: 1px solid var(--accent-dim);
    border-radius: 12px;
    color: var(--accent);
    padding: 2px 9px;
    font-size: 11px;
  }
  .chip button {
    font-size: inherit;
    margin-left: 4px;
  }
  .site-help {
    margin: 8px 2px 0;
  }
  .detection-summary {
    font-size: 13px;
    padding: 0 2px;
  }
  .detection {
    padding: 10px 12px;
    font-size: 13px;
  }
  .detected {
    list-style: none;
    margin: -8px 0 0;
    padding: 6px 10px;
    max-height: 180px;
    overflow: auto;
  }
  .detected li {
    min-height: 30px;
    padding: 3px 0;
    font-size: 12px;
  }
  .site {
    min-width: 92px;
    text-align: center;
    font-size: 11px;
    border-radius: 4px;
    padding: 3px 6px;
    background: var(--accent-dim);
  }
  .site.bad {
    background: #5c1d1d;
    color: #ffb4ad;
  }
  .album-url {
    max-width: 220px;
    font-size: 11px;
  }
  .empty-hint {
    display: flex;
    align-items: center;
    gap: 12px;
    color: var(--text);
    padding: 2px 4px;
    font-size: 13px;
  }
  .hint-icon,
  .folder-icon {
    color: var(--accent);
    font-size: 20px;
  }
  .destination {
    padding: 12px 14px;
    gap: 12px;
  }
  .destination-label {
    min-width: 0;
    display: flex;
    flex-direction: column;
    gap: 3px;
  }
  .destination-label strong {
    font-size: 12px;
  }
  .destination .out {
    max-width: min(560px, 58vw);
    font-size: 12px;
  }
  .actions {
    padding-top: 2px;
  }
  .action-main {
    gap: 10px;
  }
  .download-button {
    min-width: 140px;
    min-height: 38px;
    font-weight: 650;
  }
  .watch-label {
    font-size: 12px;
  }
  .watch-amount {
    width: 64px;
  }
  .watch-amount[aria-invalid='true'] {
    border-color: var(--err);
  }
  .action-hint {
    margin: 8px 2px 0;
  }
  .error-message,
  .running-note {
    margin: 0;
  }
  .small {
    font-size: 12px;
  }
  .spin {
    display: inline-block;
    color: var(--accent);
    animation: spin 1s linear infinite;
  }
  @keyframes spin {
    to { transform: rotate(360deg); }
  }
  @media (max-width: 640px) {
    .home {
      padding: 24px 14px;
    }
    .input-footer,
    .action-main {
      flex-wrap: wrap;
    }
    .input-footer #url-help {
      flex-basis: 100%;
    }
    .site-select {
      margin-left: auto;
    }
    .album-url {
      display: none;
    }
    .destination .out {
      max-width: 56vw;
    }
    .watch-label {
      flex-basis: 100%;
      justify-content: flex-end;
    }
  }
  @media (max-width: 420px) {
    .destination {
      align-items: flex-start;
    }
    .destination .subtle {
      white-space: normal;
    }
    .detection-summary {
      flex-wrap: wrap;
    }
    .site {
      min-width: 76px;
    }
  }
</style>
