// Functions for talking to the Go API, and the addresses of each video's
// files and chat. API requests and chat go to /api/..., and Nginx (or Vite
// while coding) passes them on to the API. Video files are under /hls/...,
// which Nginx passes to the cache. The cache fetches any file it doesn't have
// from the API, which reads it from the storage nodes.

import { titleCase } from './format'

// A video is "processing" while a worker converts it, then "ready" to play. A
// stream is "live" while it's happening, then "ready" like any other video.
export type VideoStatus = 'processing' | 'live' | 'ready'

// One video, in the shape the API sends it. duration is its length in
// seconds, or null until a worker has converted it or a stream has ended.
// created_at is a date written as text, such as "2026-11-10T22:16:08Z".
export type Video = {
  id: string
  title: string
  status: VideoStatus
  duration: number | null
  created_at: string
}

// One video's numbers, in the shape the API sends them for the creator
// dashboard. watch_seconds is the total time everyone has spent watching it.
export type VideoStats = {
  id: string
  title: string
  views: number
  watch_seconds: number
  created_at: string
}

// The address of one of a video's files, made by a worker or, for a stream,
// by the ingest service.
function hlsFileUrl(id: string, name: string): string {
  return `/hls/${encodeURIComponent(id)}/${name}`
}

// The master playlist, which lists every quality. The player starts here.
export function playlistUrl(id: string): string {
  return hlsFileUrl(id, 'master.m3u8')
}

// A stream's live playlist, which lists only the newest few segments. The
// player loads it again every few seconds to find new ones.
export function livePlaylistUrl(id: string): string {
  return hlsFileUrl(id, 'live.m3u8')
}

// A still image from the video, for thumbnails and before playback starts.
export function posterUrl(id: string): string {
  return hlsFileUrl(id, 'poster.jpg')
}

// One chat message, in the shape the API sends it.
export type ChatMessage = {
  name: string
  text: string
}

// The address of a stream's chat. A WebSocket address starts with ws://, or
// wss:// on a secure site, instead of http://.
export function chatUrl(id: string): string {
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${scheme}//${window.location.host}/api/chat/${encodeURIComponent(id)}`
}

// Fetches JSON from the API. Throws an Error if the request fails.
async function getJSON<T>(url: string): Promise<T> {
  const res = await fetch(url)
  // res.ok is true for success codes (200 to 299).
  if (!res.ok) {
    throw new Error(failureMessage(res.status, await res.text()))
  }
  return res.json()
}

// Fetches every video, newest first.
export function listVideos(): Promise<Video[]> {
  return getJSON('/api/videos')
}

// Fetches every video's views and watch time, newest first.
export function listStats(): Promise<VideoStats[]> {
  return getJSON('/api/stats')
}

// Tells the API that a video started playing.
export function reportView(id: string) {
  report(id, 'view')
}

// Tells the API how many seconds of a video were just watched.
export function reportWatch(id: string, seconds: number) {
  report(id, 'watch', { seconds })
}

// Sends a view or watch time to the API. keepalive lets the request finish
// even if the viewer is leaving the page. Counting views isn't worth
// bothering the viewer about, so a failure is ignored.
function report(id: string, kind: 'view' | 'watch', body?: object) {
  fetch(`/api/videos/${encodeURIComponent(id)}/${kind}`, {
    method: 'POST',
    keepalive: true,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  }).catch(() => {})
}

// Uploads a video and returns it as saved. It uses XMLHttpRequest instead of
// fetch because only XMLHttpRequest can report upload progress. onProgress,
// if given, is called with a number from 0 to 1 as the file is sent.
export function uploadVideo(
  title: string,
  file: File,
  onProgress?: (fraction: number) => void,
): Promise<Video> {
  // FormData builds a multipart form, the format browsers use to send files.
  const body = new FormData()
  body.append('title', title)
  body.append('file', file)

  // XMLHttpRequest reports results through callbacks. Wrapping it in a
  // Promise lets callers use `await`: resolve() gives back the result, and
  // reject() throws the error.
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', '/api/videos')

    // Runs repeatedly while the file is sent. lengthComputable is false when
    // the total size is unknown, in which case there's no progress to report.
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable) onProgress?.(event.loaded / event.total)
    }

    // Runs once the server has replied, whether it succeeded or not.
    xhr.onload = () => {
      // xhr.status is the reply's HTTP status code. 200 to 299 means success,
      // and the API sends 201 Created. Anything else, like 400 or 500, is an
      // error. xhr.responseText is the reply body as text.
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(JSON.parse(xhr.responseText)) // This turns the JSON text into an object.
      } else {
        reject(new Error(failureMessage(xhr.status, xhr.responseText)))
      }
    }

    // Runs if the request never got a reply at all, such as when offline.
    xhr.onerror = () => reject(new Error('Network Error. Check Your Connection and Try Again.'))

    xhr.send(body)
  })
}

// Gets a readable message from anything a failed call might throw.
export function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}

// Picks the message to show for a failed request.
function failureMessage(status: number, body: string): string {
  // 413 means "too large". Nginx sends it before the request reaches the API.
  if (status === 413) return 'That File Is Too Large. The Limit Is 2 GB.'
  // The API explains errors in short plain text, such as "missing title",
  // which is shown in title case. Nginx's own error pages are HTML (they start
  // with "<"), so those aren't shown.
  const text = body.trim()
  if (text && !text.startsWith('<')) return titleCase(text)
  return `Request Failed With Status ${status}`
}
