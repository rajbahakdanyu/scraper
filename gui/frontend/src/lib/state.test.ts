import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ui, initEvents } from './state.svelte'

const listeners = vi.hoisted(() => new Map<string, (event: { data: unknown }) => void>())

vi.mock('../../bindings/github.com/rajbahakdanyu/scraper/gui/backend/app', () => ({
  Notices: vi.fn(async () => []),
}))

vi.mock('@wailsio/runtime', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@wailsio/runtime')>()
  return {
    ...actual,
    Events: {
      ...actual.Events,
      On: vi.fn((name: string, callback: (event: { data: unknown }) => void) => {
        listeners.set(name, callback)
        return vi.fn()
      }),
    },
  }
})

describe('Wails runtime events', () => {
  beforeEach(() => {
    listeners.clear()
    ui.run = null
    ui.groups = []
    ui.finished = null
    ui.notices = []
    ui.unreadNotices = 0
    ui.nextRun = ''
  })

  it('updates UI state from event payloads', async () => {
    await initEvents()

    listeners.get('run:update')?.({ data: { running: true, phase: 'downloading' } })
    expect(ui.run).toMatchObject({ running: true, phase: 'downloading' })

    listeners.get('run:group')?.({ data: { group: 0, error: '', retryFile: 'retry.txt', summary: '' } })
    expect(ui.groups).toEqual([{ group: 0, error: '', retryFile: 'retry.txt', summary: '' }])

    listeners.get('notice')?.({ data: 'Download warning' })
    expect(ui.notices).toEqual(['Download warning'])
    expect(ui.unreadNotices).toBe(1)

    listeners.get('watch:next')?.({ data: '2026-01-01T00:00:00Z' })
    expect(ui.nextRun).toBe('2026-01-01T00:00:00Z')
    listeners.get('watch:stopped')?.({ data: undefined })
    expect(ui.nextRun).toBe('')
  })
})
