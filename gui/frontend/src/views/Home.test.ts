import { cleanup, fireEvent, render, waitFor } from '@testing-library/svelte'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { ui } from '../lib/state.svelte'
import Home from './Home.svelte'

const api = vi.hoisted(() => ({
  ApplySettings: vi.fn(),
  ChooseOutputDir: vi.fn(),
  DetectSites: vi.fn(),
  GetOptions: vi.fn(),
  GetSettings: vi.fn(),
  ImportURLList: vi.fn(),
  Preview: vi.fn(),
  StartRun: vi.fn(),
  StartWatch: vi.fn(),
}))

vi.mock('../../wailsjs/go/backend/App', () => api)
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }))

afterEach(cleanup)

describe('Home', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    ui.input = { text: '', fromFile: false, site: '' }
    ui.run = null
    ui.view = 'home'
    api.GetOptions.mockResolvedValue({ sites: ['youtube', 'pixeldrain'] })
    api.GetSettings.mockResolvedValue({ outputDir: '/Users/test/Downloads' })
    api.DetectSites.mockImplementation(async (text: string) =>
      text.trim() ? [{ url: text.trim(), site: 'youtube', albumUrl: '' }] : [],
    )
  })

  it('explains how to start and disables actions before links are entered', async () => {
    const { getByText, getByRole } = render(Home)
    expect(getByText('Start with a link')).toBeTruthy()
    expect((getByRole('button', { name: /Download/ }) as HTMLButtonElement).disabled).toBe(true)
    await waitFor(() => expect(getByText('/Users/test/Downloads')).toBeTruthy())
  })

  it('rechecks retained links when returning to the Download screen', async () => {
    ui.input.text = 'https://youtube.com/watch?v=retained'
    const { getByText } = render(Home)
    await waitFor(() => expect(getByText('1 supported')).toBeTruthy())
    expect(api.DetectSites).toHaveBeenCalledOnce()
  })

  it('lets a site be chosen before entering a link and keeps it through detection', async () => {
    api.DetectSites.mockImplementation(async (text: string) => [{ url: text, site: '', albumUrl: '' }])
    api.StartRun.mockResolvedValue(undefined)
    const { getByLabelText, getByRole, getByText } = render(Home)
    const site = getByLabelText('Site') as HTMLSelectElement
    await waitFor(() => expect(site.disabled).toBe(false))
    await fireEvent.change(site, { target: { value: 'pixeldrain' } })
    await fireEvent.input(getByLabelText('Links'), { target: { value: 'https://mirror.example/file' } })
    await waitFor(() => expect(getByText('Using pixeldrain')).toBeTruthy())
    expect(site.value).toBe('pixeldrain')

    await fireEvent.click(getByRole('button', { name: /Download/ }))
    await waitFor(() => expect(api.StartRun).toHaveBeenCalledWith({
      text: 'https://mirror.example/file',
      fromFile: false,
      site: 'pixeldrain',
    }))
  })

  it('switches to auto-detect and explains why for a batch', async () => {
    api.DetectSites.mockImplementation(async (text: string) =>
      text.split('\n').map((url) => ({ url, site: url.includes('youtube') ? 'youtube' : '', albumUrl: '' })),
    )
    const { getByLabelText, getByRole, getByText } = render(Home)
    const site = getByLabelText('Site') as HTMLSelectElement
    await waitFor(() => expect(site.disabled).toBe(false))
    await fireEvent.change(site, { target: { value: 'pixeldrain' } })
    await fireEvent.input(getByLabelText('Links'), {
      target: { value: 'https://youtube.com/watch?v=one\nhttps://unknown.example/file' },
    })
    await waitFor(() => expect(getByText(/1 unsupported/)).toBeTruthy())
    expect(site.disabled).toBe(true)
    expect(site.value).toBe('')
    expect(getByText(/Batches use auto-detect/)).toBeTruthy()
    expect(getByText(/Remove unsupported links/)).toBeTruthy()
    expect((getByRole('button', { name: /Download/ }) as HTMLButtonElement).disabled).toBe(true)
  })

  it('can treat a single imported link as a single download', async () => {
    api.ImportURLList.mockResolvedValue('https://mirror.example/file')
    api.DetectSites.mockResolvedValue([{ url: 'https://mirror.example/file', site: '', albumUrl: '' }])
    const { getByLabelText, getByRole } = render(Home)
    await fireEvent.click(getByRole('button', { name: /Import list/ }))
    const useSingle = await waitFor(() => getByRole('button', { name: 'Use imported link as a single download' }))
    const site = getByLabelText('Site') as HTMLSelectElement
    expect(site.disabled).toBe(true)
    await fireEvent.click(useSingle)
    expect(site.disabled).toBe(false)
    await fireEvent.change(site, { target: { value: 'pixeldrain' } })
    expect((getByRole('button', { name: /Download/ }) as HTMLButtonElement).disabled).toBe(false)
  })

  it('builds the repeat interval from the amount and unit', async () => {
    api.StartWatch.mockResolvedValue(undefined)
    const { getByLabelText, getByRole, getByText } = render(Home)
    await fireEvent.input(getByLabelText('Links'), { target: { value: 'https://youtube.com/watch?v=w' } })
    await waitFor(() => expect(getByText('1 supported')).toBeTruthy())
    const watch = getByRole('button', { name: 'Watch' }) as HTMLButtonElement
    const amount = getByLabelText('Repeat amount') as HTMLInputElement

    await fireEvent.input(amount, { target: { value: '0' } })
    expect(watch.disabled).toBe(true)
    await fireEvent.input(amount, { target: { value: '2' } })
    await fireEvent.change(getByLabelText('Repeat unit'), { target: { value: 'd' } })
    expect(watch.disabled).toBe(false)

    await fireEvent.click(watch)
    await waitFor(() => expect(api.StartWatch).toHaveBeenCalledWith(expect.anything(), '48h'))
  })

  it('explains how to start the backend when opened in a plain browser', async () => {
    api.GetOptions.mockRejectedValue(new Error('bridge missing'))
    api.DetectSites.mockRejectedValue(new Error('detection unavailable'))
    const { getByLabelText, getByRole } = render(Home)
    const alert = await waitFor(() => getByRole('alert'))
    expect(alert.textContent).toContain('Desktop backend unavailable')
    expect(alert.textContent).toContain('wails dev')
    expect(alert.textContent).toContain('bridge missing')

    await fireEvent.input(getByLabelText('Links'), { target: { value: 'https://example.com/file' } })
    await waitFor(() => expect(getByRole('alert').textContent).toContain('detection unavailable'))
    expect(getByRole('alert').textContent).toContain('wails dev')
  })

  it('ignores a slower detection result for older input', async () => {
    const resolveOld: ((result: { url: string; site: string; albumUrl: string }[]) => void)[] = []
    api.DetectSites.mockImplementation((text: string) => {
      if (text.includes('old')) {
        return new Promise((resolve) => {
          resolveOld.push(resolve)
        })
      }
      return Promise.resolve([{ url: text.trim(), site: 'youtube', albumUrl: '' }])
    })

    const { getByLabelText, getByText } = render(Home)
    const textarea = getByLabelText('Links')
    await fireEvent.input(textarea, { target: { value: 'https://old.example/video' } })
    await new Promise((resolve) => setTimeout(resolve, 300))
    expect(resolveOld.length).toBeGreaterThan(0)

    await fireEvent.input(textarea, { target: { value: 'https://youtube.com/watch?v=new' } })
    await new Promise((resolve) => setTimeout(resolve, 300))
    await waitFor(() => expect(getByText('1 supported')).toBeTruthy())
    for (const resolve of resolveOld) {
      resolve([{ url: 'https://old.example/video', site: '', albumUrl: '' }])
    }
    await new Promise((resolve) => setTimeout(resolve, 0))

    expect(getByText('https://youtube.com/watch?v=new')).toBeTruthy()
    expect(getByText('1 supported')).toBeTruthy()
  })
})
