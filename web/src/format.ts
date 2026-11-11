// Helpers that turn raw values (dates, seconds, bytes) into short text for the
// page. `undefined` as the language means "use the visitor's browser
// language", so dates appear in their own format.

// Writes phrases like "3 hours ago". numeric: 'auto' allows words such as
// "yesterday" instead of "1 day ago".
const relativeTime = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

// Each unit and its length in seconds, largest first. Months and years are
// rounded, which is fine for "how long ago".
const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 60 * 60],
  ['month', 30 * 24 * 60 * 60],
  ['week', 7 * 24 * 60 * 60],
  ['day', 24 * 60 * 60],
  ['hour', 60 * 60],
  ['minute', 60],
]

// Describes how long ago a date was, using the largest unit that fits:
// "3 hours ago", "yesterday", or "now" for under a minute.
export function timeAgo(iso: string, now = Date.now()): string {
  // Seconds between the date and now. It's negative because the date is in
  // the past, which is what tells the formatter to say "ago".
  const seconds = (new Date(iso).getTime() - now) / 1000
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) {
      return relativeTime.format(Math.round(seconds / size), unit)
    }
  }
  return relativeTime.format(0, 'second')
}

// Turns a length in seconds into "4:07", or "1:02:09" for an hour or more.
export function formatDuration(totalSeconds: number): string {
  const s = Math.floor(totalSeconds)
  const hours = Math.floor(s / 3600)
  const minutes = Math.floor((s % 3600) / 60) // % gives the remainder after dividing.
  const seconds = String(s % 60).padStart(2, '0') // For example, "7" becomes "07".
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, '0')}:${seconds}`
    : `${minutes}:${seconds}`
}

// Turns a date into short text such as "Nov 10, 2026".
export function formatDate(iso: string): string {
  return new Date(iso).toLocaleDateString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
  })
}

// Turns a byte count into text such as "12.4 MB".
export function formatFileSize(bytes: number): string {
  const sizeUnits = ['B', 'KB', 'MB', 'GB']
  let size = bytes
  let unit = 0
  // Divide by 1024 (one step up in size) until the number is under 1024.
  while (size >= 1024 && unit < sizeUnits.length - 1) {
    size /= 1024
    unit++
  }
  // One decimal place, except for plain bytes.
  return `${size.toFixed(unit === 0 ? 0 : 1)} ${sizeUnits[unit]}`
}
