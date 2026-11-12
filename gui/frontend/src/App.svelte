<script lang="ts">
  import { onMount } from 'svelte'
  import { ClearNotices } from '../bindings/github.com/rajbahakdanyu/scraper/gui/backend/app'
  import { initEvents, ui, type View } from './lib/state.svelte'
  import Home from './views/Home.svelte'
  import PreviewView from './views/PreviewView.svelte'
  import RunView from './views/RunView.svelte'
  import Settings from './views/Settings.svelte'

  let showNotices = $state(false)

  onMount(() => {
    initEvents()
  })

  function toggleNotices() {
    showNotices = !showNotices
    ui.unreadNotices = 0
  }

  async function clearNotices() {
    await ClearNotices()
    ui.notices = []
  }

  // Browser shortcuts that would reload or print the webview and lose state.
  const browserKeys = new Set(['r', 'p'])

  function onKey(e: KeyboardEvent) {
    const mod = e.metaKey || e.ctrlKey
    if (e.key === 'F5' || (mod && browserKeys.has(e.key.toLowerCase()))) {
      e.preventDefault()
      return
    }
    if (e.key === 'Escape' && showNotices) {
      showNotices = false
      return
    }
    if (!mod || e.altKey || e.shiftKey) return
    const views: Record<string, () => boolean> = {
      '1': () => go('home'),
      '2': () => !!ui.run && go('run'),
      '3': () => go('settings'),
      ',': () => go('settings'),
    }
    if (views[e.key]?.()) e.preventDefault()
  }

  function go(view: View) {
    ui.view = view
    return true
  }

  // Dropping a file would navigate the webview to it, like a browser tab.
  function blockFileDrop(e: DragEvent) {
    if (!e.dataTransfer?.types.includes('Files')) return
    e.preventDefault()
    e.dataTransfer.dropEffect = 'none'
  }
</script>

<svelte:window onkeydown={onKey} ondragover={blockFileDrop} ondrop={blockFileDrop} />

<div class="shell">
  <header class="row">
    <span class="logo">◢◤ SCRAPER ◢◤</span>
    <nav class="row" aria-label="Main navigation">
      <button class:active={ui.view === 'home' || ui.view === 'preview'} aria-current={ui.view === 'home' || ui.view === 'preview' ? 'page' : undefined} onclick={() => (ui.view = 'home')}>Download</button>
      <button class:active={ui.view === 'run'} aria-current={ui.view === 'run' ? 'page' : undefined} disabled={!ui.run} onclick={() => (ui.view = 'run')}>
        Run{#if ui.run?.running}<span class="dot"></span>{/if}
      </button>
      <button class:active={ui.view === 'settings'} aria-current={ui.view === 'settings' ? 'page' : undefined} onclick={() => (ui.view = 'settings')}>Settings</button>
    </nav>
    <span class="grow"></span>
    <button onclick={toggleNotices} title="Warnings from scrapes and filters">
      Notices{#if ui.unreadNotices > 0}<span class="badge">{ui.unreadNotices}</span>{/if}
    </button>
  </header>

  <main>
    {#if ui.view === 'home'}
      <Home />
    {:else if ui.view === 'preview'}
      <PreviewView />
    {:else if ui.view === 'run'}
      <RunView />
    {:else}
      <Settings />
    {/if}
  </main>

  {#if showNotices}
    <aside class="notices panel">
      <div class="row">
        <strong class="grow">Notices ({ui.notices.length})</strong>
        <button onclick={clearNotices}>Clear</button>
        <button onclick={() => (showNotices = false)}>Close</button>
      </div>
      <div class="list mono">
        {#each ui.notices as n, i (i)}
          <div>{n}</div>
        {:else}
          <div class="dim">No notices.</div>
        {/each}
      </div>
    </aside>
  {/if}

  {#if ui.toast}
    <div class="toast panel">{ui.toast}</div>
  {/if}
</div>

<style>
  .shell {
    display: flex;
    flex-direction: column;
    height: 100%;
  }
  header {
    padding: 8px 14px;
    border-bottom: 1px solid var(--border);
    background: var(--panel);
    --wails-draggable: drag;
  }
  header button {
    --wails-draggable: no-drag;
  }
  .logo {
    color: var(--accent);
    font-weight: 700;
    letter-spacing: 1px;
    margin-right: 16px;
  }
  nav button {
    background: none;
    border-color: transparent;
  }
  nav button.active {
    border-color: var(--accent);
    color: var(--accent);
  }
  @media (max-width: 520px) {
    header {
      padding: 8px;
      gap: 5px;
    }
    .logo {
      font-size: 11px;
      letter-spacing: 0;
      margin-right: 2px;
      white-space: nowrap;
      flex-shrink: 0;
    }
    nav {
      gap: 2px;
    }
    nav button,
    header > button {
      padding: 5px 7px;
      font-size: 12px;
    }
  }
  @media (max-width: 380px) {
    .logo {
      display: none;
    }
  }
  .dot {
    display: inline-block;
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--accent);
    margin-left: 6px;
    vertical-align: middle;
  }
  .badge {
    background: var(--warn);
    color: #000;
    border-radius: 8px;
    padding: 0 6px;
    margin-left: 6px;
    font-size: 11px;
  }
  main {
    flex: 1;
    min-height: 0;
    overflow: auto;
    overscroll-behavior: none;
  }
  .notices {
    position: fixed;
    right: 12px;
    top: 52px;
    width: min(560px, 90vw);
    max-height: 60vh;
    display: flex;
    flex-direction: column;
    padding: 10px;
    gap: 8px;
    z-index: 20;
  }
  .notices .list {
    overflow: auto;
    font-size: 12px;
    user-select: text;
  }
  .toast {
    position: fixed;
    bottom: 16px;
    left: 50%;
    transform: translateX(-50%);
    padding: 8px 14px;
    z-index: 30;
    max-width: 80vw;
  }
</style>
