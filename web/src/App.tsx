import { useEffect, useState } from 'react'
import { errorMessage, listVideos, type Video } from './api'
import { Link } from './components/Link'
import { UploadForm } from './components/UploadForm'
import { VideoGrid, VideoGridSkeleton } from './components/VideoGrid'
import { WatchPage } from './components/WatchPage'
import { useWatchId } from './router'

export default function App() {
  const watchId = useWatchId()

  const [videos, setVideos] = useState<Video[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  const [reloadKey, setReloadKey] = useState(0)
  // Loads the video list again. Changing reloadKey re-runs the effect that
  // fetches the list. Called after each upload.
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

  const watching = watchId ? videos?.find((v) => v.id === watchId) : undefined

  useEffect(() => {
    document.title = watching ? `${watching.title} - Flick` : 'Flick'
  }, [watching])

  // Draws the list on the home page: an error, loading placeholders, or the
  // videos.
  function renderVideos() {
    // Show the error only if nothing has loaded yet. If an older list exists,
    // keep showing it instead.
    if (error && !videos) {
      return (
        <div className="empty" role="alert">
          <h2>Couldn't load videos</h2>
          <p>{error}</p>
          <button type="button" className="button button-tonal" onClick={reload}>
            Try again
          </button>
        </div>
      )
    }
    if (!videos) return <VideoGridSkeleton />
    return <VideoGrid videos={videos} />
  }

  // Draws the watch page: the player, or a message if the video doesn't exist.
  function renderWatch() {
    if (!videos) return null // The list is still loading, so show nothing.
    if (!watching) {
      return (
        <div className="empty">
          <h2>This video isn't available</h2>
          <Link href="/" className="button button-tonal">
            Back to home
          </Link>
        </div>
      )
    }
    return <WatchPage video={watching} others={videos.filter((v) => v.id !== watching.id)} />
  }

  return (
    <main className="container page">
      {watchId ? (
        renderWatch()
      ) : (
        <>
          <UploadForm onUploaded={reload} />
          {renderVideos()}
        </>
      )}
    </main>
  )
}
