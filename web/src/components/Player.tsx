import Hls from 'hls.js/light'
import { useEffect, useRef } from 'react'
import { playlistUrl, posterUrl } from '../api'

type Props = {
  id: string
}

// Plays a video with adaptive streaming. Most browsers can't play HLS on
// their own, so hls.js downloads the segments and feeds them to the <video>
// element. The "light" build leaves out features Flick doesn't use, such as
// subtitles and copy protection, which makes it much smaller. It also times each download and picks the quality the connection
// can keep up with. Safari plays HLS on its own, so there it's given the
// playlist directly.
export function Player({ id }: Props) {
  const ref = useRef<HTMLVideoElement>(null)

  useEffect(() => {
    const video = ref.current
    if (!video) return
    const src = playlistUrl(id)

    if (!Hls.isSupported()) {
      video.src = src
      return
    }

    const hls = new Hls()
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
  }, [id])

  return (
    <video ref={ref} className="player" poster={posterUrl(id)} controls autoPlay playsInline />
  )
}
