import { useEffect, useState } from 'react'
import { errorMessage, listVideos, type Video } from './api'
import { Dashboard } from './components/Dashboard'
import { Link } from './components/Link'
import { UploadDialog } from './components/UploadDialog'
import { VideoGrid, VideoGridSkeleton } from './components/VideoGrid'
import { WatchPage } from './components/WatchPage'
import { dashboardUrl, navigate, usePathname, useWatchId } from './router'

// The list loads again this often while a video is processing or live.
const POLL_INTERVAL_MS = 3000

export default function App() {
  const watchId = useWatchId()
  const onDashboard = usePathname() === dashboardUrl

  const [videos, setVideos] = useState<Video[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [uploadOpen, setUploadOpen] = useState(false)

  const [reloadKey, setReloadKey] = useState(0)
  // Loads the video list again. Changing reloadKey runs the effect that
  // fetches the list again. It's called after each upload, and while videos
  // are processing or live.
  const reload = () => setReloadKey((key) => key + 1)

  useEffect(() => {
    let stale = false
    listVideos().then(
      (list) => {
        if (stale) return
        setVideos(list)
        setError(null)
      },
      (err: unknown) => {
        if (!stale) setError(errorMessage(err))
      },
    )
    return () => {
      stale = true
    }
  }, [reloadKey])

  // While any video is still processing or live, load the list again every
  // few seconds, so its status changes on screen without a refresh. Each load
  // changes reloadKey, which runs this effect again and sets the next timer.
  // Once every video is ready, the timers stop.
  const changing = videos?.some((v) => v.status === 'processing' || v.status === 'live') ?? false
  useEffect(() => {
    if (!changing) return
    const timer = setTimeout(reload, POLL_INTERVAL_MS)
    return () => clearTimeout(timer)
  }, [changing, reloadKey])

  const watching = watchId ? videos?.find((v) => v.id === watchId) : undefined

  useEffect(() => {
    if (watching) document.title = `${watching.title} - Flick`
    else if (onDashboard) document.title = 'Creator Dashboard - Flick'
    else document.title = 'Flick'
  }, [watching, onDashboard])

  // Draws the message shown when the list failed to load, with a button to try
  // again. It's shown only if nothing has loaded yet. If an older list exists,
  // the page keeps showing it instead.
  function renderLoadError() {
    return (
      <div className="empty" role="alert">
        <h2>Couldn't Load Videos</h2>
        <p>{error}</p>
        <button type="button" className="button button-tonal" onClick={reload}>
          Try Again
        </button>
      </div>
    )
  }

  // Draws the list on the home page: an error, loading placeholders, or the
  // videos.
  function renderVideos() {
    if (error && !videos) return renderLoadError()
    if (!videos) return <VideoGridSkeleton />
    return <VideoGrid videos={videos} onUpload={() => setUploadOpen(true)} />
  }

  // Draws the watch page: the player, or a message if the video doesn't exist.
  function renderWatch() {
    if (error && !videos) return renderLoadError()
    if (!videos) return null // The list is still loading, so show nothing.
    if (!watching) {
      return (
        <div className="empty">
          <h2>This Video Isn't Available</h2>
          <Link href="/" className="button button-tonal">
            Back to Home
          </Link>
        </div>
      )
    }
    return <WatchPage video={watching} others={videos.filter((v) => v.id !== watching.id)} />
  }

  // Picks the page to draw from the address.
  function renderPage() {
    if (watchId) return renderWatch()
    if (onDashboard) return <Dashboard />
    return renderVideos()
  }

  // Shows the new video on the home page once it has uploaded, with the list
  // loaded again so it's there. On the home page already, it only reloads, so
  // the back button doesn't gain an extra step.
  function handleUploaded() {
    if (watchId || onDashboard) navigate('/')
    reload()
  }

  // Every page has the same header: the name, which links home, a button that
  // opens the upload form, and a link to the creator dashboard.
  return (
    <>
      <header className="header">
        <nav className="container header-inner" aria-label="Main">
          <Link href="/" className="wordmark">
            Flick
          </Link>
          <div className="header-actions">
            <button type="button" className="button button-tonal" onClick={() => setUploadOpen(true)}>
              Upload
            </button>
            <Link
              href={dashboardUrl}
              className="button button-text"
              aria-current={onDashboard ? 'page' : undefined}
            >
              Dashboard
            </Link>
          </div>
        </nav>
      </header>
      <main className="container page">{renderPage()}</main>
      <UploadDialog open={uploadOpen} onClose={() => setUploadOpen(false)} onUploaded={handleUploaded} />
    </>
  )
}
