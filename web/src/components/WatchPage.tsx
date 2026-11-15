import { lazy, Suspense } from 'react'
import type { Video } from '../api'
import { formatDate } from '../format'
import { watchUrl } from '../router'
import { Chat } from './Chat'
import { Link } from './Link'
import { Thumbnail } from './Thumbnail'
import { VideoMeta } from './VideoMeta'

// The player uses hls.js, which is large, and only this page needs it. lazy()
// loads it the first time the player is shown, so the home page loads faster.
const Player = lazy(() => import('./Player').then((module) => ({ default: module.Player })))

type Props = {
  video: Video
  others: Video[]
}

export function WatchPage({ video, others }: Props) {
  return (
    <div className="watch">
      <div className="watch-main">
        {/* The player only appears once a worker has converted the video. The
            list keeps reloading until then, so it appears without a refresh.
            A stream plays as soon as it's live, and switches to the finished
            video once it ends. */}
        {video.status === 'processing' ? (
          <div className="player player-pending" role="status">
            <p>This Video Is Still Processing. It Will Play Here Once It's Ready.</p>
          </div>
        ) : (
          // Suspense shows an empty player box while hls.js loads.
          <Suspense fallback={<div className="player" />}>
            <Player key={video.id} id={video.id} live={video.status === 'live'} />
          </Suspense>
        )}
        <h1 className="watch-title">{video.title}</h1>
        <div className="description">
          <strong>{formatDate(video.created_at)}</strong>
        </div>
      </div>

      {/* A stream has its chat beside it. Any other video has the other
          videos beside it. */}
      {video.status === 'live' ? (
        <Chat key={video.id} id={video.id} />
      ) : (
        others.length > 0 && (
          <aside className="up-next" aria-label="More Videos">
            <ul>
              {others.map((other) => (
                <li key={other.id}>
                  <Link href={watchUrl(other.id)} className="compact-card">
                    <Thumbnail video={other} />
                    <div>
                      <h3 className="card-title">{other.title}</h3>
                      <VideoMeta video={other} />
                    </div>
                  </Link>
                </li>
              ))}
            </ul>
          </aside>
        )
      )}
    </div>
  )
}
