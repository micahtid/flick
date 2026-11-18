import { useEffect, useState } from 'react'
import { errorMessage, listStats, type VideoStats } from '../api'
import { formatDate, formatWatchTime } from '../format'
import { watchUrl } from '../router'
import { Link } from './Link'

// The stats load again this often. The API adds new views to them once a
// minute, so each update shows up here within 30 seconds.
const RELOAD_INTERVAL_MS = 30_000

// Shows the creator dashboard: every video's views and watch time, newest
// first.
export function Dashboard() {
  const [stats, setStats] = useState<VideoStats[] | null>(null)
  const [error, setError] = useState<string | null>(null)

  // Loads the stats now, then again every 30 seconds while the page is open.
  useEffect(() => {
    let stale = false
    function load() {
      listStats().then(
        (list) => {
          if (stale) return
          setStats(list)
          setError(null)
        },
        (err: unknown) => {
          if (!stale) setError(errorMessage(err))
        },
      )
    }
    load()
    const timer = setInterval(load, RELOAD_INTERVAL_MS)
    return () => {
      stale = true
      clearInterval(timer)
    }
  }, [])

  // Draws the table, or a message if there's nothing to show yet.
  function renderStats() {
    // Show the error only if nothing has loaded yet. If older stats exist,
    // keep showing them instead.
    if (error && !stats) {
      return (
        <div className="empty" role="alert">
          <h2>Couldn't Load the Numbers</h2>
          <p>{error}</p>
        </div>
      )
    }
    if (!stats) return null // The stats are still loading, so show nothing.
    if (stats.length === 0) {
      return (
        <div className="empty">
          <h2>No Videos Yet</h2>
          <p>Upload a Video, and Its Views Show Up Here.</p>
        </div>
      )
    }
    return (
      <div className="stats-scroll">
        <table className="stats">
          <thead>
            <tr>
              <th scope="col">Video</th>
              <th scope="col">Views</th>
              <th scope="col">Watch Time</th>
              <th scope="col">Uploaded</th>
            </tr>
          </thead>
          <tbody>
            {stats.map((video) => (
              <tr key={video.id}>
                <td>
                  <Link href={watchUrl(video.id)}>{video.title}</Link>
                </td>
                <td>{video.views.toLocaleString()}</td>
                <td>{formatWatchTime(video.watch_seconds)}</td>
                <td>{formatDate(video.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    )
  }

  return (
    <div className="dashboard">
      <h1 className="dashboard-title">Creator Dashboard</h1>
      {renderStats()}
    </div>
  )
}
