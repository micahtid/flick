import Hls from 'hls.js/light'
import { useEffect, useRef } from 'react'
import { livePlaylistUrl, playlistUrl, posterUrl } from '../api'

// How many seconds behind the newest segment a stream plays. Staying a few
// segments back leaves room to buffer, so playback doesn't stall.
const LIVE_SYNC_SECONDS = 6

type Props = {
  id: string
  live: boolean
}

// Plays a video with adaptive streaming. Most browsers can't play HLS on
// their own, so hls.js downloads the segments and feeds them to the <video>
// element. It also times each download and picks the quality the connection
// can keep up with. The "light" build leaves out features Flick doesn't use,
// such as subtitles and copy protection, which makes it much smaller. Safari
// plays HLS on its own, so there it's given the playlist directly.
//
// A stream plays from its live playlist instead, which hls.js loads again
// every few seconds to find new segments.
export function Player({ id, live }: Props) {
  const ref = useRef<HTMLVideoElement>(null)

  useEffect(() => {
    const video = ref.current
    if (!video) return
    const src = live ? livePlaylistUrl(id) : playlistUrl(id)

    if (!Hls.isSupported()) {
      video.src = src
      return
    }

    // liveSyncDuration only affects live playlists.
    const hls = new Hls({ liveSyncDuration: LIVE_SYNC_SECONDS })
    // Most errors are retried by hls.js itself. A fatal one stops playback,
    // so try to recover once: reload for a network error, or reset the
    // decoder for a media error.
    hls.on(Hls.Events.ERROR, (_event, data) => {
      if (!data.fatal) return
      if (data.type === Hls.ErrorTypes.NETWORK_ERROR) hls.startLoad()
      else if (data.type === Hls.ErrorTypes.MEDIA_ERROR) hls.recoverMediaError()
      else hls.destroy()
    })
    hls.loadSource(src)
    hls.attachMedia(video)
    return () => hls.destroy() // Stop downloading when the player goes away.
  }, [id, live])

  return (
    <video ref={ref} className="player" poster={posterUrl(id)} controls autoPlay playsInline />
  )
}
