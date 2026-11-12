import { Notices } from '../../bindings/github.com/rajbahakdanyu/scraper/gui/backend/app'
import type * as backend from '../../bindings/github.com/rajbahakdanyu/scraper/gui/backend/models'
import { Events } from '@wailsio/runtime'

export type View = 'home' | 'run' | 'preview' | 'settings'

export type GroupResult = { group: number; error: string; retryFile: string; summary: string }
export type RunResult = { error: string; partial: boolean }

// ui is the app-wide state shared by every view.
export const ui = $state({
  view: 'home' as View,
  run: null as backend.RunState | null,
  groups: [] as GroupResult[],
  finished: null as RunResult | null,
  notices: [] as string[],
  unreadNotices: 0,
  nextRun: '',
  input: { text: '', fromFile: false, site: '' },
  preview: null as backend.PreviewAlbum[] | null,
  toast: '',
})

let toastTimer: ReturnType<typeof setTimeout> | undefined

export function toast(msg: string) {
  ui.toast = msg
  clearTimeout(toastTimer)
  toastTimer = setTimeout(() => (ui.toast = ''), 4000)
}

export function errorText(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

// resetRun clears the previous run's results before a new one starts.
export function resetRun() {
  ui.groups = []
  ui.finished = null
}

export async function initEvents() {
  ui.notices = (await Notices()) ?? []
  Events.On('run:update', (event) => {
    ui.run = event.data as backend.RunState
  })
  Events.On('run:group', (event) => {
    ui.groups = [...ui.groups, event.data as GroupResult]
  })
  Events.On('run:finished', (event) => {
    ui.finished = event.data as RunResult
  })
  Events.On('notice', (event) => {
    ui.notices = [...ui.notices.slice(-199), event.data as string]
    ui.unreadNotices++
  })
  Events.On('watch:next', (event) => {
    ui.nextRun = event.data as string
  })
  Events.On('watch:stopped', () => {
    ui.nextRun = ''
  })
}
