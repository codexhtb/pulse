function locale(): string {
  return localStorage.getItem('pulse-language') === 'en' ? 'en-GB' : 'ru-RU'
}

export function formatCompact(value: number): string {
  return new Intl.NumberFormat(locale(), {
    notation: value >= 10_000 ? 'compact' : 'standard',
    maximumFractionDigits: value < 10 ? 1 : 0,
  }).format(Number.isFinite(value) ? value : 0)
}

export function formatPercent(value: number): string {
  return `${(Number.isFinite(value) ? value : 0).toFixed(value >= 10 ? 1 : 2)}%`
}

export function formatLatency(microseconds: number | null): string {
  if (microseconds === null || !Number.isFinite(microseconds)) return '—'
  if (microseconds < 1000) return `${Math.round(microseconds)} µs`
  if (microseconds < 1_000_000) return `${(microseconds / 1000).toFixed(1)} ms`
  return `${(microseconds / 1_000_000).toFixed(2)} s`
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
  return `${(bytes / 1024 ** index).toFixed(index === 0 ? 0 : 1)} ${units[index]}`
}

export function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat(locale(), {
    day: '2-digit',
    month: 'short',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date)
}

export function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat(locale(), {
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    fractionalSecondDigits: 3,
    hour12: false,
  }).format(date)
}

export function relativeTime(value: string): string {
  const delta = new Date(value).getTime() - Date.now()
  if (!Number.isFinite(delta)) return localStorage.getItem('pulse-language') === 'en' ? 'unknown' : 'неизвестно'
  const absolute = Math.abs(delta)
  const formatter = new Intl.RelativeTimeFormat(locale(), { numeric: 'auto' })

  if (absolute < 60_000) return formatter.format(Math.round(delta / 1000), 'second')
  if (absolute < 3_600_000) return formatter.format(Math.round(delta / 60_000), 'minute')
  if (absolute < 86_400_000) return formatter.format(Math.round(delta / 3_600_000), 'hour')
  return formatter.format(Math.round(delta / 86_400_000), 'day')
}

export function sourceStatus(lastSeen: string): 'active' | 'delayed' | 'silent' {
  const age = Date.now() - new Date(lastSeen).getTime()
  if (age < 5 * 60_000) return 'active'
  if (age < 15 * 60_000) return 'delayed'
  return 'silent'
}
