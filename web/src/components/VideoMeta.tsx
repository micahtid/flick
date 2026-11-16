import type { Video, VideoStatus } from '../api'
import { timeAgo } from '../format'

// These statuses get a small label before the upload time.
const STATUS_LABELS: Partial<Record<VideoStatus, string>> = {
  processing: 'Processing',
  failed: 'Failed',
}

type Props = {
  video: Video
}

// Shows the line under a video's title: a "Processing" label while a worker
// is still converting it, or "Failed" if it couldn't, then how long ago it
// was uploaded.
export function VideoMeta({ video }: Props) {
  const label = STATUS_LABELS[video.status]
  return (
    <p className="meta">
      {label && <span className="status">{label}</span>}
      {timeAgo(video.created_at)}
    </p>
  )
}
