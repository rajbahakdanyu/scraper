import { cleanup, fireEvent, render } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ui } from './lib/state.svelte'
import App from './App.svelte'

// Views load data on mount; leaving the calls pending keeps them inert.
vi.mock('../bindings/github.com/rajbahakdanyu/scraper/gui/backend/app', () => {
  const pending = () => vi.fn(() => new Promise(() => {}))
  return {
    ClearNotices: pending(),
    DetectSites: pending(),
    GetOptions: pending(),
    GetSettings: pending(),
    Notices: pending(),
  }
})
vi.mock('@wailsio/runtime', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@wailsio/runtime')>()
  return { ...actual, Events: { ...actual.Events, On: vi.fn() } }
})

afterEach(cleanup)

describe('App native behavior', () => {
  beforeEach(() => {
    ui.view = 'home'
    ui.run = null
  })

  it('switches views with ⌘/Ctrl shortcuts', async () => {
    render(App)
    await fireEvent.keyDown(window, { key: ',', metaKey: true })
    expect(ui.view).toBe('settings')
    await fireEvent.keyDown(window, { key: '1', ctrlKey: true })
    expect(ui.view).toBe('home')
    await fireEvent.keyDown(window, { key: '2', metaKey: true })
    expect(ui.view).toBe('home')
  })

  it('blocks browser reload and print', () => {
    render(App)
    for (const init of [{ key: 'F5' }, { key: 'r', metaKey: true }, { key: 'R', ctrlKey: true, shiftKey: true }, { key: 'p', metaKey: true }]) {
      const e = new KeyboardEvent('keydown', { ...init, cancelable: true })
      window.dispatchEvent(e)
      expect(e.defaultPrevented, JSON.stringify(init)).toBe(true)
    }
  })

  it('stops files dropped on the window from opening in the webview', () => {
    render(App)
    const drop = (types: string[]) => {
      const e = new Event('drop', { cancelable: true }) as DragEvent
      Object.defineProperty(e, 'dataTransfer', { value: { types, dropEffect: 'copy' } })
      window.dispatchEvent(e)
      return e.defaultPrevented
    }
    expect(drop(['Files'])).toBe(true)
    expect(drop(['text/plain'])).toBe(false)
  })
})
