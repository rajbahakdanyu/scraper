import { Notices } from '../../wailsjs/go/backend/App'
import type { backend } from '../../wailsjs/go/models'
import { EventsOn } from '../../wailsjs/runtime/runtime'

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
  EventsOn('run:update', (state: backend.RunState) => {
    ui.run = state
  })
  EventsOn('run:group', (g: GroupResult) => {
    ui.groups = [...ui.groups, g]
  })
  EventsOn('run:finished', (r: RunResult) => {
    ui.finished = r
  })
  EventsOn('notice', (msg: string) => {
    ui.notices = [...ui.notices.slice(-199), msg]
    ui.unreadNotices++
  })
  EventsOn('watch:next', (at: string) => {
    ui.nextRun = at
  })
  EventsOn('watch:stopped', () => {
    ui.nextRun = ''
  })
}
