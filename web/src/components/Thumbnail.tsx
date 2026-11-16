import { posterUrl, type Video } from '../api'
import { formatDuration } from '../format'
import { VideoIcon } from './icons'

type Props = {
  video: Video
}

export function Thumbnail({ video }: Props) {
  // There's no poster to show until a worker has converted the video, or if
  // it failed, so show a plain placeholder instead. A stream has a poster as
  // soon as it's live.
  if (video.status === 'processing' || video.status === 'failed') {
    return (
      <div className="thumbnail thumbnail-pending">
        <VideoIcon size={32} />
      </div>
    )
  }

  return (
    <div className="thumbnail">
      {/* The title is shown next to the image, so it needs no alt text.
          loading="lazy" waits to fetch images until they're near the screen. */}
      <img src={posterUrl(video.id)} alt="" loading="lazy" />
      {/* A stream shows a "Live" badge in place of its length, which isn't
          known until it ends. The length is null for videos converted by an
          older version of Flick, or when FFmpeg couldn't tell. */}
      {video.status === 'live' ? (
        <span className="duration live">Live</span>
      ) : (
        video.duration !== null && <span className="duration">{formatDuration(video.duration)}</span>
      )}
    </div>
  )
}
