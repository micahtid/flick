import type { Video } from '../api'
import { watchUrl } from '../router'
import { Link } from './Link'
import { Thumbnail } from './Thumbnail'
import { VideoMeta } from './VideoMeta'
import { VideoIcon } from './icons'

type Props = {
  videos: Video[]
}

export function VideoGrid({ videos }: Props) {
  if (videos.length === 0) {
    return (
      <div className="empty">
        <VideoIcon size={64} />
        <h2>No Videos Yet</h2>
        <p>Upload Your First Video Above to Get Started.</p>
      </div>
    )
  }

  return (
    <ul className="grid">
      {videos.map((video) => (
        <li key={video.id}>
          <Link href={watchUrl(video.id)} className="card">
            <Thumbnail video={video} />
            <h3 className="card-title">{video.title}</h3>
            <VideoMeta video={video} />
          </Link>
        </li>
      ))}
    </ul>
  )
}

const SKELETON_COUNT = 8

export function VideoGridSkeleton() {
  return (
    <ul className="grid" aria-busy="true" aria-label="Loading Videos">
      {Array.from({ length: SKELETON_COUNT }, (_, i) => (
        <li key={i} className="skeleton">
          <div className="thumbnail" />
          <div className="skeleton-line" />
          <div className="skeleton-line short" />
        </li>
      ))}
    </ul>
  )
}
