// This file turns raw values, such as dates, seconds and bytes, into short
// text for the page. `undefined` as the language means "use the viewer's
// browser language", so dates appear in their own format.

// relativeTime writes phrases such as "3 hours ago". numeric: 'auto' allows
// words such as "yesterday" instead of "1 day ago".
const relativeTime = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' })

// units lists each unit and its length in seconds, largest first. Months and
// years are rounded, which is fine for "how long ago".
const units: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 60 * 60],
  ['month', 30 * 24 * 60 * 60],
  ['week', 7 * 24 * 60 * 60],
  ['day', 24 * 60 * 60],
  ['hour', 60 * 60],
  ['minute', 60],
]

// Describes how long ago a date was, using the largest unit that fits:
// "3 Hours Ago", "Yesterday" or "Now" for under a minute.
export function timeAgo(iso: string, now = Date.now()): string {
  // This is the time in seconds between the date and now. It's negative
  // because the date is in the past, which tells the formatter to say "ago".
  const seconds = (new Date(iso).getTime() - now) / 1000
  for (const [unit, size] of units) {
    if (Math.abs(seconds) >= size) {
      return titleCase(relativeTime.format(Math.round(seconds / size), unit))
    }
  }
  return titleCase(relativeTime.format(0, 'second'))
}

// These short words stay lowercase in title case, unless they start a
// sentence.
const minorWords = new Set(['a', 'an', 'and', 'as', 'at', 'but', 'by', 'for', 'in', 'of', 'on', 'or', 'the', 'to'])

// Puts text in title case, so every word starts with a capital letter except
// the short words above: "try again in a minute" becomes "Try Again in a
// Minute". All text on the website uses title case. This is for text made while
// the website runs, such as dates and the API's error messages.
export function titleCase(text: string): string {
  // [A-Za-z][\w']* matches one word, including words such as "isn't".
  return text.replace(/[A-Za-z][\w']*/g, (word, offset: number) => {
    // A word starts a sentence if it's first, or comes after ".", "!" or "?".
    const startsSentence = offset === 0 || /[.!?]\s*$/.test(text.slice(0, offset))
    if (!startsSentence && minorWords.has(word.toLowerCase())) return word.toLowerCase()
    return word[0].toUpperCase() + word.slice(1)
  })
}

// Turns a length in seconds into "4:07", or "1:02:09" for an hour or more.
export function formatDuration(totalSeconds: number): string {
  const s = Math.floor(totalSeconds)
  const hours = Math.floor(s / 3600)
  const minutes = Math.floor((s % 3600) / 60) // % gives the remainder after dividing.
  const seconds = String(s % 60).padStart(2, '0') // This turns "7" into "07".
  return hours > 0
    ? `${hours}:${String(minutes).padStart(2, '0')}:${seconds}`
    : `${minutes}:${seconds}`
}

// Turns watch time in seconds into hours and minutes, such as "3 Hr 12 Min",
// or "12 Min" for under an hour.
export function formatWatchTime(totalSeconds: number): string {
  const hours = Math.floor(totalSeconds / 3600)
  const minutes = Math.floor((totalSeconds % 3600) / 60)
  return hours > 0 ? `${hours} Hr ${minutes} Min` : `${minutes} Min`
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
  // Show one decimal place, except for plain bytes.
  return `${size.toFixed(unit === 0 ? 0 : 1)} ${sizeUnits[unit]}`
}
