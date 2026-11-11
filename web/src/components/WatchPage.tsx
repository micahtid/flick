import { videoFileUrl, type Video } from '../api'
import { formatDate, timeAgo } from '../format'
import { watchUrl } from '../router'
import { Link } from './Link'
import { Thumbnail } from './Thumbnail'

type Props = {
  video: Video
  others: Video[]
}

export function WatchPage({ video, others }: Props) {
  return (
    <div className="watch">
      <div className="watch-main">
        <video
          key={video.id}
          className="player"
          src={videoFileUrl(video.id)}
          controls
          autoPlay
          playsInline
        />
        <h1 className="watch-title">{video.title}</h1>
        <div className="description">
          <strong>{formatDate(video.created_at)}</strong>
        </div>
      </div>

      {others.length > 0 && (
        <aside className="up-next" aria-label="More videos">
          <ul>
            {others.map((other) => (
              <li key={other.id}>
                <Link href={watchUrl(other.id)} className="compact-card">
                  <Thumbnail videoId={other.id} />
                  <div>
                    <h3 className="card-title">{other.title}</h3>
                    <p className="meta">{timeAgo(other.created_at)}</p>
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
