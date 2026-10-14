import { fireEvent, render } from '@testing-library/svelte'
import { describe, expect, it, vi } from 'vitest'
import ItemRow from './ItemRow.svelte'

const base = {
  row: 0,
  index: 1,
  header: false,
  album: '',
  site: '',
  title: 'clip.mp4',
  status: 'downloading',
  bytes: 512,
  total: 1024,
  speed: 2048,
  queuePos: 0,
  folded: false,
  done: 0,
  failed: 0,
  count: 0,
  error: '',
  hasPath: false,
}

function props(row: Partial<typeof base>, handlers: Record<string, () => void> = {}) {
  const noop = () => {}
  return {
    row: { ...base, ...row } as never,
    top: 0,
    height: 30,
    selected: false,
    mark: '',
    onselect: handlers.onselect ?? noop,
    onfold: handlers.onfold ?? noop,
    oncancel: handlers.oncancel ?? noop,
    onretry: handlers.onretry ?? noop,
    oninspect: handlers.oninspect ?? noop,
    onreveal: handlers.onreveal ?? noop,
    oncopy: handlers.oncopy ?? noop,
  }
}

describe('ItemRow', () => {
  it('shows progress, percent and speed while downloading', () => {
    const { container } = render(ItemRow, props({}))
    expect(container.textContent).toContain('50%')
    expect(container.textContent).toContain('2.0K/s')
    expect(container.textContent).toContain('Cancel')
  })

  it('shows the queue position for pending items', () => {
    const { container } = render(ItemRow, props({ status: 'pending', queuePos: 3 }))
    expect(container.textContent).toContain('queued #3')
  })

  it('offers retry and error inspection for failed items', async () => {
    const onretry = vi.fn()
    const { getByText, container } = render(ItemRow, props({ status: 'failed', error: 'boom' }, { onretry }))
    expect(container.textContent).toContain('✗ failed')
    expect(container.textContent).not.toContain('Cancel')
    await fireEvent.click(getByText('Retry'))
    expect(onretry).toHaveBeenCalledOnce()
  })

  it('renders album headers with counts and toggles the fold on click', async () => {
    const onfold = vi.fn()
    const { container } = render(
      ItemRow,
      props({ header: true, album: 'album1', site: 'bunker', done: 1, count: 4, failed: 1, folded: true }, { onfold }),
    )
    expect(container.textContent).toContain('▸')
    expect(container.textContent).toContain('1/4')
    expect(container.textContent).toContain('(1 failed)')
    await fireEvent.click(container.querySelector('.item')!)
    expect(onfold).toHaveBeenCalledOnce()
  })
})
