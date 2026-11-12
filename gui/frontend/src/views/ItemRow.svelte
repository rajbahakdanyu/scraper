<script lang="ts">
  import type * as backend from '../../bindings/github.com/rajbahakdanyu/scraper/gui/backend/models'
  import { humanBytes, percent } from '../lib/format'

  type Props = {
    row: backend.RowView
    top: number
    height: number
    selected: boolean
    mark: string
    retryDisabled?: boolean
    onselect: () => void
    onfold: () => void
    oncancel: () => void
    onretry: () => void
    oninspect: () => void
    onreveal: () => void
    oncopy: () => void
  }

  let { row, top, height, selected, mark, retryDisabled = false, onselect, onfold, oncancel, onretry, oninspect, onreveal, oncopy }: Props = $props()

  const pct = $derived(percent(row.bytes, row.total))
  const live = $derived(row.status === 'pending' || row.status === 'downloading')
</script>

<!-- svelte-ignore a11y_click_events_have_key_events -->
<div
  class="item row"
  class:header={row.header}
  class:selected
  style:top="{top}px"
  style:height="{height}px"
  role="row"
  tabindex="-1"
  onclick={row.header ? () => { onselect(); onfold() } : onselect}
>
  {#if row.header}
    <span class="c-mark accent">{row.folded ? '▸' : '▾'}</span>
    <span class="grow ellipsis accent"><b>{row.album}</b> <span class="dim">({row.site})</span></span>
    <span class="c-status mono">{row.done}/{row.count}</span>
    <span class="c-progress row">
      <span class="bar grow"><span style:width="{percent(row.done, row.count)}%"></span></span>
      <span class="mono small">{percent(row.done, row.count)}%{#if row.failed > 0} <span class="err">({row.failed} failed)</span>{/if}</span>
    </span>
    <span class="c-actions"></span>
  {:else}
    <span class="c-mark" class:spin={row.status === 'downloading'} class:ok={row.status === 'completed'} class:err={row.status === 'failed'}>{mark}</span>
    <span class="grow ellipsis" title={row.title}>{row.title}</span>
    <span class="c-status small" class:err={row.status === 'failed'}>{row.status}</span>
    <span class="c-progress row mono small">
      {#if row.status === 'pending'}
        <span class="dim">queued #{row.queuePos}</span>
      {:else if row.status === 'failed'}
        <span class="err">✗ failed</span>
      {:else if row.status === 'cancelled'}
        <span class="dim">⊘ cancelled</span>
      {:else if row.status === 'completed'}
        <span class="bar grow"><span style:width="100%"></span></span><span>100%</span>
      {:else if row.total > 0}
        <span class="bar grow"><span style:width="{pct}%"></span></span>
        <span>{pct}%</span>
        {#if row.speed > 0}<span class="dim">{humanBytes(Math.floor(row.speed))}/s</span>{/if}
      {:else}
        <span class="dim">{humanBytes(row.bytes)} · ???</span>
      {/if}
    </span>
    <span class="c-actions row actions">
      {#if live}<button onclick={(e) => { e.stopPropagation(); oncancel() }}>Cancel</button>{/if}
      {#if row.status === 'failed'}
        <button disabled={retryDisabled} onclick={(e) => { e.stopPropagation(); onretry() }}>Retry</button>
        <button onclick={(e) => { e.stopPropagation(); oninspect() }}>Error</button>
      {/if}
      {#if row.hasPath}<button onclick={(e) => { e.stopPropagation(); onreveal() }}>Show</button>{/if}
      <button onclick={(e) => { e.stopPropagation(); oncopy() }} title="Copy item URL">Copy</button>
    </span>
  {/if}
</div>

<style>
  .item {
    position: absolute;
    left: 0;
    right: 0;
    padding: 0 10px;
    gap: 8px;
    border-bottom: 1px solid rgba(255, 255, 255, 0.03);
    cursor: default;
  }
  .item:hover {
    background: rgba(34, 211, 238, 0.05);
  }
  .item.selected {
    background: rgba(34, 211, 238, 0.12);
  }
  .item.header {
    background: var(--panel-2);
    cursor: default;
  }
  .accent {
    color: var(--accent);
  }
  .small {
    font-size: 12px;
  }
  .bar {
    display: block;
    height: 6px;
    background: var(--panel-2);
    border-radius: 3px;
    overflow: hidden;
  }
  .bar > span {
    display: block;
    height: 100%;
    background: var(--accent);
  }
  .actions button {
    padding: 1px 8px;
    font-size: 12px;
    visibility: hidden;
  }
  .item:hover .actions button,
  .item.selected .actions button {
    visibility: visible;
  }
  .spin {
    color: var(--accent);
    animation: pulse 1s ease-in-out infinite;
  }
  @keyframes pulse {
    50% {
      opacity: 0.3;
    }
  }
</style>
