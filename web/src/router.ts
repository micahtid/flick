// This file is a small router for the three pages: home ("/"), watch
// ("/watch?v=<id>") and the creator dashboard ("/dashboard"). The page is read
// from the address bar, so links can be shared and the back button works. Nginx
// and Vite send index.html for any path, so opening a /watch link directly also
// works.

import { useSyncExternalStore } from 'react'

// This is the name of our own browser event, which navigate() fires whenever
// it changes page.
const NAVIGATE_EVENT = 'flick:navigate'

// Calls onChange whenever the address may have changed, and returns a
// function that stops listening. The browser fires "popstate" for back and
// forward, but has no event for pushState, so navigate() fires our own.
function subscribe(onChange: () => void) {
  window.addEventListener('popstate', onChange)
  window.addEventListener(NAVIGATE_EVENT, onChange)
  return () => {
    window.removeEventListener('popstate', onChange)
    window.removeEventListener(NAVIGATE_EVENT, onChange)
  }
}

// Reads the video ID from the address, such as "abc" from "/watch?v=abc".
// Any other page gives null.
function currentWatchId(): string | null {
  return window.location.pathname === '/watch'
    ? new URLSearchParams(window.location.search).get('v') // search is the "?v=abc" part of the address.
    : null
}

// This React hook returns the ID of the video being watched, or null on any
// other page. useSyncExternalStore re-renders the component whenever
// subscribe reports a change.
export function useWatchId(): string | null {
  return useSyncExternalStore(subscribe, currentWatchId)
}

// This React hook returns the page's path, such as "/" or "/dashboard".
export function usePathname(): string {
  return useSyncExternalStore(subscribe, () => window.location.pathname)
}

// This is the address of the creator dashboard.
export const dashboardUrl = '/dashboard'

// Returns the address of a video's watch page. encodeURIComponent escapes
// characters that would break the address.
export function watchUrl(id: string): string {
  return `/watch?v=${encodeURIComponent(id)}`
}

// Changes page without reloading the website.
export function navigate(url: string) {
  window.history.pushState(null, '', url) // This updates the address bar and the back-button history.
  window.dispatchEvent(new Event(NAVIGATE_EVENT)) // This tells the hooks above to read the address again.
  window.scrollTo(0, 0) // This starts at the top, the same as a normal page load.
}
