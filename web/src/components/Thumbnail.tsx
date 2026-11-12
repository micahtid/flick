import { useState } from 'react'
import { videoFileUrl, type Video } from '../api'
import { formatDuration } from '../format'
import { VideoIcon } from './icons'

type Props = {
  video: Video
}

export function Thumbnail({ video }: Props) {
  const [duration, setDuration] = useState<number | null>(null)

  // There's no file to show until a worker has converted the video, so show
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
      <video
        src={`${videoFileUrl(video.id)}#t=0.5`}
        preload="metadata"
        muted
        playsInline
        tabIndex={-1}
        onLoadedMetadata={(event) => setDuration(event.currentTarget.duration)}
      />
      {duration !== null && Number.isFinite(duration) && (
        <span className="duration">{formatDuration(duration)}</span>
      )}
    </div>
  )
}
