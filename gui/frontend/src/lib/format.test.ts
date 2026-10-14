import { describe, expect, it } from 'vitest'
import { duration, humanBytes, percent, splitList } from './format'

describe('humanBytes', () => {
  it('matches the TUI formatting', () => {
    expect(humanBytes(-1)).toBe('?')
    expect(humanBytes(512)).toBe('512B')
    expect(humanBytes(1024)).toBe('1.0K')
    expect(humanBytes(3 * 1024 * 1024)).toBe('3.0M')
    expect(humanBytes(1536 * 1024 * 1024)).toBe('1.5G')
  })
})

describe('duration', () => {
  it('renders like Go durations', () => {
    expect(duration(0)).toBe('0s')
    expect(duration(59)).toBe('59s')
    expect(duration(630)).toBe('10m30s')
    expect(duration(3723)).toBe('1h2m3s')
  })
})

describe('percent', () => {
  it('clamps and handles unknown totals', () => {
    expect(percent(5, 0)).toBe(0)
    expect(percent(50, 100)).toBe(50)
    expect(percent(150, 100)).toBe(100)
  })
})

describe('splitList', () => {
  it('accepts commas and newlines', () => {
    expect(splitList(' *.mp4, *.mkv\n\n*.zip ')).toEqual(['*.mp4', '*.mkv', '*.zip'])
  })
})
