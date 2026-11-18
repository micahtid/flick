import { MdOutlinePlayCircle } from 'react-icons/md'
import type { Video } from '../api'
import { watchUrl } from '../router'
import { Link } from './Link'
import { Thumbnail } from './Thumbnail'
import { VideoMeta } from './VideoMeta'

type Props = {
  videos: Video[]
  onUpload: () => void
}

export function VideoGrid({ videos, onUpload }: Props) {
  if (videos.length === 0) {
    return (
      <div className="empty">
        <MdOutlinePlayCircle size={48} aria-hidden="true" />
        <h2>No Videos Yet</h2>
        <p>Upload Your First Video to Get Started.</p>
        <button type="button" className="button button-filled" onClick={onUpload}>
          Upload a Video
        </button>
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

const SKELETON_COUNT = 6

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
