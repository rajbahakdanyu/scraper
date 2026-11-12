import { cleanup, fireEvent, render } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { backend } from '../../wailsjs/go/models'
import { ui } from '../lib/state.svelte'
import RunView from './RunView.svelte'

const api = vi.hoisted(() => {
  const pending = () => vi.fn(() => new Promise(() => {}))
  return {
    CancelAll: pending(),
    CancelItem: pending(),
    ContinueRun: pending(),
    DismissSummary: pending(),
    ErrorLog: pending(),
    FindNext: pending(),
    ItemURL: pending(),
    OpenOutputFolder: pending(),
    RetryAllFailed: pending(),
    RetryItem: pending(),
    RevealItem: pending(),
    RevealRetryList: pending(),
    Rows: vi.fn(async () => []),
    RunState: pending(),
    SaveRetryList: pending(),
    SetFilter: pending(),
    StopRun: pending(),
    StopWatch: pending(),
    ToggleFold: pending(),
  }
})

vi.mock('../../wailsjs/go/backend/App', () => api)
vi.mock('../../wailsjs/runtime/runtime', () => ({ ClipboardSetText: vi.fn() }))

// jsdom lacks ResizeObserver, which Svelte's size bindings use.
globalThis.ResizeObserver ??= class {
  observe() {}
  unobserve() {}
  disconnect() {}
} as unknown as typeof ResizeObserver

afterEach(cleanup)

describe('RunView keys', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ui.view = 'run'
    ui.run = {
      running: true,
      phase: 'downloading',
      visible: 1,
      stats: { total: 1, done: 0, failed: 0 },
      version: 1,
    } as unknown as backend.RunState
  })

  it('leaves ⌘/Ctrl/Alt combinations to the system', async () => {
    render(RunView)
    await fireEvent.keyDown(window, { key: 'c', metaKey: true })
    await fireEvent.keyDown(window, { key: 'c', ctrlKey: true })
    await fireEvent.keyDown(window, { key: 'r', metaKey: true })
    await fireEvent.keyDown(window, { key: 'C', altKey: true })
    expect(api.CancelItem).not.toHaveBeenCalled()
    expect(api.RetryItem).not.toHaveBeenCalled()
    expect(api.CancelAll).not.toHaveBeenCalled()

    await fireEvent.keyDown(window, { key: 'c' })
    expect(api.CancelItem).toHaveBeenCalledOnce()
  })
})
