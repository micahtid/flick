import { useState } from 'react'
import { videoFileUrl } from '../api'
import { formatDuration } from '../format'

type Props = {
  videoId: string
}

export function Thumbnail({ videoId }: Props) {
  const [duration, setDuration] = useState<number | null>(null)

  return (
    <div className="thumbnail">
      <video
        src={`${videoFileUrl(videoId)}#t=0.5`}
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
