import { videoFileUrl, type Video } from '../api'
import { formatDate } from '../format'
import { watchUrl } from '../router'
import { Link } from './Link'
import { Thumbnail } from './Thumbnail'
import { VideoMeta } from './VideoMeta'

type Props = {
  video: Video
  others: Video[]
}

export function WatchPage({ video, others }: Props) {
  return (
    <div className="watch">
      <div className="watch-main">
        {/* The player only appears once a worker has converted the video. The
            list keeps reloading until then, so it appears without a refresh. */}
        {video.status === 'ready' ? (
          <video
            key={video.id}
            className="player"
            src={videoFileUrl(video.id)}
            controls
            autoPlay
            playsInline
          />
        ) : (
          <div className="player player-pending" role="status">
            <p>This Video Is Still Processing. It Will Play Here Once It's Ready.</p>
          </div>
        )}
        <h1 className="watch-title">{video.title}</h1>
        <div className="description">
          <strong>{formatDate(video.created_at)}</strong>
        </div>
      </div>

      {others.length > 0 && (
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
      )}
    </div>
  )
}
