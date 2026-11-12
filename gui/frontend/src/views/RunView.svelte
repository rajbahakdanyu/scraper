<script lang="ts">
  import {
      CancelAll,
      CancelItem,
      ContinueRun,
      DismissSummary,
      ErrorLog,
      FindNext,
      ItemURL,
      OpenOutputFolder,
      RetryAllFailed,
      RetryItem,
      RevealItem,
      RevealRetryList,
      Rows,
      RunState,
      SaveRetryList,
      SetFilter,
      StopRun,
      StopWatch,
      ToggleFold,
  } from '../../wailsjs/go/backend/App'
  import type { backend } from '../../wailsjs/go/models'
  import { ClipboardSetText } from '../../wailsjs/runtime/runtime'
  import { duration, humanBytes, percent, statusMarks } from '../lib/format'
  import { errorText, toast, ui } from '../lib/state.svelte'
  import ItemRow from './ItemRow.svelte'

  const ROW_H = 30
  const OVERSCAN = 12
  const FILTERS = ['all', 'failed', 'active', 'pending']

  let list = $state<HTMLDivElement>()
  let searchBox = $state<HTMLInputElement>()
  let rows = $state<backend.RowView[]>([])
  let scrollTop = $state(0)
  let viewH = $state(400)
  let selected = $state({ row: 0, index: -1 })
  let query = $state('')
  let showErrors = $state(false)
  let errors = $state<string[]>([])
  let inspect = $state('')
  let showStatus = $state(false)
  let statusTimer: ReturnType<typeof setTimeout> | undefined
  let fetching = false
  let dirty = false
  let loadingRun = false

  const run = $derived(ui.run)
  const stats = $derived(run?.stats)
  const total = $derived(run?.visible ?? 0)
  const active = $derived(!!run?.active)
  const scrapeFailed = $derived(run?.phase === 'done' && !!run.loadingErr && run.visible === 0)

  $effect(() => {
    if (!ui.run && !loadingRun) {
      loadingRun = true
      RunState().then((s) => {
        ui.run = s
      }).catch((err) => {
        toast(errorText(err))
      }).finally(() => {
        loadingRun = false
      })
    }
  })

  // Refetch the visible window whenever the run changes or the list scrolls.
  $effect(() => {
    void run?.version
    void scrollTop
    void viewH
    fetchRows()
  })

  $effect(() => {
    if (!run?.statusSeq) return
    showStatus = true
    clearTimeout(statusTimer)
    statusTimer = setTimeout(() => (showStatus = false), 3000)
  })

  $effect(() => {
    if (showErrors && run) {
      void run.errors
      ErrorLog().then((e) => (errors = e ?? [])).catch((err) => {
        errors = []
        toast(errorText(err))
      })
    }
  })

  async function fetchRows() {
    if (fetching) {
      dirty = true
      return
    }
    fetching = true
    try {
      do {
        dirty = false
        const start = Math.max(0, Math.floor(scrollTop / ROW_H) - OVERSCAN)
        const count = Math.ceil(viewH / ROW_H) + 2 * OVERSCAN
        rows = (await Rows(start, count)) ?? []
        const hit = rows.find((r) => r.index === selected.index)
        if (hit) selected = { row: hit.row, index: hit.index }
      } while (dirty)
    } catch (err) {
      toast(errorText(err))
    } finally {
      fetching = false
    }
  }

  const selectedRow = $derived(rows.find((r) => r.row === selected.row))

  function select(row: number, index = -1, scroll = true) {
    row = Math.max(0, Math.min(row, total - 1))
    const hit = rows.find((r) => r.row === row)
    selected = { row, index: hit ? hit.index : index }
    inspect = ''
    if (scroll && list) {
      const top = row * ROW_H
      if (top < list.scrollTop) list.scrollTop = top
      else if (top + ROW_H > list.scrollTop + viewH) list.scrollTop = top - viewH + ROW_H
    }
  }

  function centre(m: backend.Match) {
    if (m.row < 0) return toast('No match')
    selected = { row: m.row, index: m.index }
    if (list) list.scrollTop = Math.max(0, m.row * ROW_H - viewH / 2)
  }

  async function guard(fn: () => Promise<unknown>) {
    try {
      await fn()
    } catch (err) {
      toast(errorText(err))
    }
  }

  const nextMatch = () => query && guard(async () => centre(await FindNext(query, false, selected.index)))
  const nextFailed = () => guard(async () => centre(await FindNext('', true, selected.index)))
  const cycleFilter = () => setFilter(FILTERS[(FILTERS.indexOf(run?.filter ?? 'all') + 1) % FILTERS.length])
  const setFilter = (f: string) => guard(() => SetFilter(f))

  function inspectSelected() {
    const r = selectedRow
    inspect = r && r.status === 'failed' ? r.error : ''
  }

  async function copyURL(index: number) {
    await guard(async () => {
      const url = await ItemURL(index)
      if (url && (await ClipboardSetText(url))) toast('URL copied')
    })
  }

  const saveRetryList = () =>
    guard(async () => {
      const path = await SaveRetryList()
      toast(path ? `Retry list written: ${path}` : 'Nothing failed — no retry list needed')
    })

  async function stop() {
    if (active && !confirm('Cancel the running downloads and end this run?')) return
    await guard(ui.nextRun ? StopWatch : StopRun)
  }

  // closeGroup mirrors quitting a TUI dashboard: the group's downloads stop
  // and the next group (if any) starts.
  async function closeGroup() {
    if (active && !confirm('Cancel the running downloads in this group?')) return
    await guard(ContinueRun)
  }

  function onKey(e: KeyboardEvent) {
    if (ui.view !== 'run' || !run) return
    const target = e.target as HTMLElement
    if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT') {
      if (e.key === 'Escape') {
        query = ''
        target.blur()
      }
      return
    }
    if (run.phase === 'summary') {
      const summaryKeys: Record<string, () => unknown> = {
        Enter: () => guard(DismissSummary),
        R: () => stats?.failed && guard(RetryAllFailed),
        L: saveRetryList,
        q: () => guard(ContinueRun),
      }
      const fn = summaryKeys[e.key]
      if (fn) {
        e.preventDefault()
        fn()
      }
      return
    }
    const idx = selected.index
    const keys: Record<string, () => unknown> = {
      ArrowDown: () => select(selected.row + 1),
      j: () => select(selected.row + 1),
      ArrowUp: () => select(selected.row - 1),
      k: () => select(selected.row - 1),
      c: () => guard(() => CancelItem(idx)),
      C: () => guard(CancelAll),
      r: () => run.running && guard(() => RetryItem(idx)),
      R: () => run.running && guard(RetryAllFailed),
      F: nextFailed,
      '/': () => searchBox?.focus(),
      n: nextMatch,
      f: cycleFilter,
      Tab: () => guard(() => ToggleFold(idx)),
      e: () => (showErrors = !showErrors),
      Enter: inspectSelected,
      q: () => run.running && closeGroup(),
    }
    const fn = keys[e.key]
    if (fn) {
      e.preventDefault()
      fn()
    }
  }
</script>

<svelte:window onkeydown={onKey} />

{#if !run}
  <p class="dim pad">No run yet.</p>
{:else}
  <section class="run">
    <div class="row head">
      <span class="ellipsis grow">
        <strong>{run.site || '…'}</strong>
        <span class="dim">→ <span class="mono">{run.dest}</span></span>
        {#if run.groups > 1}<span class="dim"> · group {run.group + 1}/{run.groups}</span>{/if}
      </span>
      {#if ui.nextRun}
        <span class="dim">watching · next run {new Date(ui.nextRun).toLocaleTimeString()}</span>
      {/if}
      <button onclick={() => guard(OpenOutputFolder)}>Open folder</button>
      {#if run.running || ui.nextRun}
        <button class="danger" onclick={stop}>{ui.nextRun ? 'Stop watching' : 'Stop'}</button>
      {/if}
    </div>

    {#if stats && !scrapeFailed}
      <div class="progress">
        <div class="bar"><div style:width="{percent(stats.doneBytes, stats.totalBytes)}%"></div></div>
        <span class="mono small">
          {#if stats.totalBytes > 0}
            {humanBytes(stats.doneBytes)}/{humanBytes(stats.totalBytes)} {percent(stats.doneBytes, stats.totalBytes)}%{stats.unknown > 0 ? '~' : ''}
          {:else if stats.unknown > 0}
            unknown size
          {/if}
        </span>
      </div>
      <div class="row stats mono small">
        {#if active}<span class="spin">◆</span>{:else if stats.failed > 0}<span class="err">✗</span>{:else}<span class="ok">✓</span>{/if}
        <span>done <b>{stats.completed}</b>/{stats.total}</span>
        <span>active <b class="accent">{stats.active}</b></span>
        <span>failed <b class="err">{stats.failed}</b></span>
        {#if run.filter !== 'all'}<span>filter <b class="accent">{run.filter}</b></span>{/if}
        {#if stats.speed > 0}
          <span>speed <b class="accent">{humanBytes(Math.floor(stats.speed))}/s</b></span>
          {#if stats.etaSeconds > 0}<span>eta <b class="accent">{duration(stats.etaSeconds)}</b></span>{/if}
        {/if}
        {#if run.batchMode && run.albumsScraped < run.albumsTotal}
          <span class="dim">scraping {run.albumsScraped}/{run.albumsTotal}</span>
        {:else if run.batchMode && run.albumsTotal > 1}
          <span class="dim">{run.albumsTotal} albums</span>
        {/if}
      </div>
    {/if}

    {#if run.phase === 'loading'}
      <div class="panel center"><span class="spin">◆</span> {run.scrapingAlbum ? `scraping ${run.scrapingAlbum}…` : 'fetching media items…'}</div>
    {:else if scrapeFailed}
      <div class="panel center err">
        ✗ {run.loadingErr}
        {#if run.running}
          <div><button onclick={() => guard(ContinueRun)}>{run.group + 1 < run.groups ? 'Next group' : 'Close'} <kbd>q</kbd></button></div>
        {/if}
      </div>
    {:else}
      <div class="row toolbar">
        <div class="row chips">
          {#each FILTERS as f (f)}
            <button class:on={run.filter === f} onclick={() => setFilter(f)}>{f}</button>
          {/each}
        </div>
        <input
          type="text"
          bind:this={searchBox}
          bind:value={query}
          placeholder="Search titles  /"
          onkeydown={(e) => e.key === 'Enter' && nextMatch()}
        />
        <button disabled={!query} onclick={nextMatch}>Next <kbd>n</kbd></button>
        <button disabled={!stats?.failed} onclick={nextFailed}>Next failed <kbd>F</kbd></button>
        <span class="grow"></span>
        <button disabled={!active} onclick={() => guard(CancelAll)}>Cancel all <kbd>C</kbd></button>
        <button disabled={!run.running || !stats?.failed} onclick={() => guard(RetryAllFailed)}>Retry failed <kbd>R</kbd></button>
        <button class:on={showErrors} onclick={() => (showErrors = !showErrors)}>Errors ({run.errors}) <kbd>e</kbd></button>
      </div>

      <div class="table panel">
        <div class="thead row mono small dim">
          <span class="c-mark"></span><span class="grow">FILE</span><span class="c-status">STATUS</span><span class="c-progress">PROGRESS</span><span class="c-actions"></span>
        </div>
        <div class="viewport" bind:this={list} bind:clientHeight={viewH} onscroll={() => (scrollTop = list?.scrollTop ?? 0)}>
          <div style:height="{total * ROW_H}px" class="spacer">
            {#each rows as r (r.row)}
              <ItemRow
                row={r}
                top={r.row * ROW_H}
                height={ROW_H}
                selected={r.row === selected.row}
                mark={statusMarks[r.status] ?? ''}
                retryDisabled={!run.running}
                onselect={() => select(r.row, r.index, false)}
                onfold={() => guard(() => ToggleFold(r.index))}
                oncancel={() => guard(() => CancelItem(r.index))}
                onretry={() => guard(() => RetryItem(r.index))}
                oninspect={() => (inspect = r.error)}
                onreveal={() => guard(() => RevealItem(r.index))}
                oncopy={() => guard(() => copyURL(r.index))}
              />
            {/each}
          </div>
        </div>
      </div>
    {/if}

    {#if showStatus && run.status}<p class="dim small status">{run.status}</p>{/if}
    {#if inspect}<p class="err small status mono">✗ {inspect} <button class="link" onclick={() => (inspect = '')}>dismiss</button></p>{/if}
    {#if showErrors}
      <div class="panel errors mono small">
        <strong>Errors ({errors.length})</strong>
        {#each errors as line, i (i)}<div class="err">{line}</div>{:else}<div class="dim">No scrape errors.</div>{/each}
      </div>
    {/if}

    <p class="keys dim small">
      <kbd>↑↓/jk</kbd> select · <kbd>c/C</kbd> cancel · <kbd>r/R</kbd> retry · <kbd>F</kbd> failed · <kbd>/</kbd> search ·
      <kbd>f</kbd> filter · <kbd>Tab</kbd> fold · <kbd>e</kbd> errors · <kbd>Enter</kbd> inspect · <kbd>q</kbd> close
    </p>
  </section>

  {#if run.phase === 'summary' && run.running}
    <div class="overlay">
      <div class="panel dialog">
        <h3>{run.groups > 1 ? `Group ${run.group + 1} of ${run.groups} finished` : 'Downloads finished'}</h3>
        <p class="mono">
          {stats?.completed} completed, {stats?.failed} failed, {stats?.cancelled} cancelled
          {#if !stats?.failed}<span class="ok"> ✓ nothing to redo</span>{/if}
        </p>
        {#if showStatus && run.status}<p class="dim small">{run.status}</p>{/if}
        <div class="row">
          <button onclick={() => guard(DismissSummary)}>View list <kbd>Enter</kbd></button>
          <button disabled={!stats?.failed} onclick={() => guard(RetryAllFailed)}>Retry failed <kbd>R</kbd></button>
          <button onclick={saveRetryList}>Write retry list <kbd>L</kbd></button>
          <span class="grow"></span>
          <button class="primary" onclick={() => guard(ContinueRun)}>
            {run.group + 1 < run.groups ? 'Next group' : 'Close'} <kbd>q</kbd>
          </button>
        </div>
      </div>
    </div>
  {/if}

  {#if !run.running && ui.finished}
    <div class="finished panel">
      {#if !ui.finished.error}
        <strong class="ok">✓ Run complete</strong>
      {:else if ui.finished.partial}
        <strong class="warn">Run finished with failures</strong> <span class="dim">{ui.finished.error}</span>
      {:else}
        <strong class="err">Run failed</strong> <span class="dim">{ui.finished.error}</span>
      {/if}
      {#each ui.groups.filter((g) => g.retryFile) as g (g.retryFile)}
        <div class="row small">
          <span class="mono ellipsis grow">Retry list: {g.retryFile}</span>
          <button onclick={() => guard(() => RevealRetryList(g.retryFile))}>Show</button>
        </div>
      {/each}
      <div class="row">
        <span class="grow"></span>
        {#if run.phase === 'summary'}<button onclick={() => guard(DismissSummary)}>View list</button>{/if}
        <button class="primary" onclick={() => (ui.view = 'home')}>New download</button>
      </div>
    </div>
  {/if}
{/if}

<style>
  .pad {
    padding: 18px;
  }
  .run {
    display: flex;
    flex-direction: column;
    gap: 8px;
    padding: 12px 16px;
    height: 100%;
  }
  .head strong {
    color: var(--accent);
  }
  .progress {
    display: grid;
    grid-template-columns: 1fr auto;
    gap: 10px;
    align-items: center;
  }
  .small {
    font-size: 12px;
    margin: 0;
  }
  .stats {
    gap: 14px;
    flex-wrap: wrap;
  }
  .accent {
    color: var(--accent);
  }
  .spin {
    display: inline-block;
    color: var(--accent);
    animation: spin 1s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  .center {
    padding: 24px;
    text-align: center;
  }
  .toolbar {
    flex-wrap: wrap;
  }
  .chips button {
    padding: 4px 10px;
    text-transform: capitalize;
  }
  button.on {
    border-color: var(--accent);
    color: var(--accent);
  }
  .table {
    flex: 1;
    min-height: 160px;
    display: flex;
    flex-direction: column;
    overflow: hidden;
  }
  .thead {
    padding: 6px 10px;
    border-bottom: 1px solid var(--border);
  }
  .viewport {
    flex: 1;
    overflow-y: auto;
    position: relative;
  }
  .spacer {
    position: relative;
  }
  :global(.c-mark) {
    width: 22px;
    text-align: center;
  }
  :global(.c-status) {
    width: 96px;
  }
  :global(.c-progress) {
    width: 230px;
  }
  :global(.c-actions) {
    width: 190px;
    justify-content: flex-end;
  }
  .status {
    user-select: text;
  }
  .errors {
    max-height: 180px;
    overflow: auto;
    padding: 8px 10px;
    user-select: text;
  }
  .keys {
    margin: 0;
  }
  .overlay {
    position: fixed;
    inset: 0;
    background: rgba(0, 0, 0, 0.55);
    display: grid;
    place-items: center;
    z-index: 10;
  }
  .dialog {
    padding: 18px 22px;
    min-width: 520px;
  }
  .dialog h3 {
    margin: 0 0 8px;
  }
  .finished {
    position: fixed;
    right: 16px;
    bottom: 16px;
    width: min(560px, 92vw);
    padding: 12px 14px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    z-index: 9;
  }
</style>
