// Formatting shared by every view; humanBytes matches the TUI's
// fileutil.HumanBytes so both frontends print sizes identically.

export function humanBytes(n: number): string {
  if (n < 0) return '?'
  const unit = 1024
  if (n < unit) return `${n}B`
  let div = unit
  let exp = 0
  for (let next = Math.floor(n / unit); next >= unit; next = Math.floor(next / unit)) {
    div *= unit
    exp++
  }
  return `${(n / div).toFixed(1)}${'KMGTPE'[exp]}`
}

// duration renders whole seconds like Go's time.Duration.String().
export function duration(seconds: number): string {
  let s = Math.round(seconds)
  if (s <= 0) return '0s'
  const h = Math.floor(s / 3600)
  s -= h * 3600
  const m = Math.floor(s / 60)
  s -= m * 60
  if (h > 0) return `${h}h${m}m${s}s`
  if (m > 0) return `${m}m${s}s`
  return `${s}s`
}

export function percent(done: number, total: number): number {
  if (total <= 0) return 0
  return Math.min(100, Math.floor((100 * done) / total))
}

export const statusMarks: Record<string, string> = {
  downloading: '◆',
  completed: '✓',
  failed: '✗',
  cancelled: '⊘',
  pending: '·',
}

// splitList turns comma- or newline-separated text into trimmed entries.
export function splitList(text: string): string[] {
  return text
    .split(/[\n,]/)
    .map((s) => s.trim())
    .filter((s) => s !== '')
}
