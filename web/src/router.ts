// A small router for the two pages: home ("/") and watch ("/watch?v=<id>").
// The page is read from the address bar, so links can be shared and the back
// button works. Nginx and Vite send index.html for any path, so opening a
// /watch link directly also works.

import { useSyncExternalStore } from 'react'

// The name of our own browser event, fired whenever navigate() changes page.
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

// A React hook that returns the ID of the video being watched, or null on the
// home page. useSyncExternalStore re-renders the component whenever
// subscribe reports a change.
export function useWatchId(): string | null {
  return useSyncExternalStore(subscribe, currentWatchId)
}

// The address of a video's watch page. encodeURIComponent escapes characters
// that would break the address.
export function watchUrl(id: string): string {
  return `/watch?v=${encodeURIComponent(id)}`
}

// Changes page without reloading the site.
export function navigate(url: string) {
  window.history.pushState(null, '', url) // This updates the address bar and the back-button history.
  window.dispatchEvent(new Event(NAVIGATE_EVENT)) // This tells useWatchId to read the address again.
  window.scrollTo(0, 0) // This starts at the top, like a normal page load.
}
