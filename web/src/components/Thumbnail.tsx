import { posterUrl, type Video } from '../api'
import { formatDuration } from '../format'
import { VideoIcon } from './icons'

type Props = {
  video: Video
}

export function Thumbnail({ video }: Props) {
  // There's no poster to show until a worker has converted the video, so show
  // a plain placeholder instead.
  if (video.status !== 'ready') {
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
      {/* The length is null for videos converted before Phase 3, or when
          FFmpeg couldn't tell. */}
      {video.duration !== null && <span className="duration">{formatDuration(video.duration)}</span>}
    </div>
  )
}
