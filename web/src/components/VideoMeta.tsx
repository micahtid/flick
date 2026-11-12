import type { Video } from '../api'
import { timeAgo } from '../format'

type Props = {
  video: Video
}

// The line under a video's title: a "Processing" label while a worker is
// still converting it, then how long ago it was uploaded.
export function VideoMeta({ video }: Props) {
  return (
    <p className="meta">
      {video.status === 'processing' && <span className="status">Processing</span>}
      {timeAgo(video.created_at)}
    </p>
  )
}
