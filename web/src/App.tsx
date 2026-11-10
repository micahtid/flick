import { useEffect, useState } from 'react'

type Status = 'checking' | 'ok' | 'down'

export default function App() {
  const [api, setApi] = useState<Status>('checking')

  useEffect(() => {
    fetch('/api/health')
      .then((res) => setApi(res.ok ? 'ok' : 'down'))
      .catch(() => setApi('down'))
  }, [])

  return (
    <main>
      <h1>Flick</h1>
      <p>A video platform built from the storage up.</p>
      <p>
        API status: <strong className={api}>{api}</strong>
      </p>
    </main>
  )
}
