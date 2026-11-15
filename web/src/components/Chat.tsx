import { useEffect, useRef, useState, type SubmitEvent } from 'react'
import { chatUrl, type ChatMessage } from '../api'

// The longest message allowed, the same limit as the API.
const MAX_MESSAGE_LENGTH = 500
// Only the newest messages are kept, so a long stream doesn't fill memory.
const MAX_MESSAGES = 200
// How long to wait before joining again after the connection drops.
const RECONNECT_DELAY_MS = 3000

// Each visit gets a guest name, such as "Guest 4821", to chat as.
const guestName = `Guest ${Math.floor(1000 + Math.random() * 9000)}`

type Props = {
  id: string
}

// A message on screen. key tells React which message is which, since older
// ones drop off the top.
type Line = ChatMessage & { key: number }

// Live chat beside a stream. It keeps a WebSocket open to the API, shows each
// message as it arrives, and sends what the viewer types. Messages aren't
// saved, so a viewer only sees ones sent after they joined.
export function Chat({ id }: Props) {
  const socketRef = useRef<WebSocket | null>(null)
  const listRef = useRef<HTMLOListElement>(null)
  const nextKey = useRef(0)
  const [messages, setMessages] = useState<Line[]>([])
  const [text, setText] = useState('')
  const [connected, setConnected] = useState(false)

  // Joins the chat, and joins again a few seconds after the connection drops,
  // such as when the API restarts.
  useEffect(() => {
    let leaving = false
    let timer: number | undefined

    function join() {
      const socket = new WebSocket(chatUrl(id))
      socketRef.current = socket
      socket.onopen = () => setConnected(true)
      socket.onmessage = (event) => {
        const line: Line = { ...JSON.parse(event.data), key: nextKey.current++ }
        setMessages((list) => [...list, line].slice(-MAX_MESSAGES))
      }
      socket.onclose = () => {
        setConnected(false)
        if (!leaving) timer = window.setTimeout(join, RECONNECT_DELAY_MS)
      }
    }

    join()
    return () => {
      leaving = true
      clearTimeout(timer)
      socketRef.current?.close()
    }
  }, [id])

  // Keeps the newest message in view.
  useEffect(() => {
    const list = listRef.current
    if (list) list.scrollTop = list.scrollHeight
  }, [messages])

  // Sends the message, and clears the box for the next one.
  function handleSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault() // This stops the browser's own submit, which would reload the page.
    const trimmed = text.trim()
    if (!trimmed || !connected) return
    socketRef.current?.send(JSON.stringify({ name: guestName, text: trimmed }))
    setText('')
  }

  return (
    <aside className="chat" aria-label="Live Chat">
      <h2 className="chat-title">Live Chat</h2>
      <ol ref={listRef} className="chat-messages" aria-live="polite">
        {messages.length === 0 ? (
          <li className="meta">No Messages Yet. Say Hello!</li>
        ) : (
          messages.map((line) => (
            <li key={line.key}>
              <strong>{line.name}</strong> {line.text}
            </li>
          ))
        )}
      </ol>
      <form className="chat-form" onSubmit={handleSubmit}>
        <label className="field">
          <span className="visually-hidden">Message</span>
          <input
            value={text}
            onChange={(event) => setText(event.target.value)}
            maxLength={MAX_MESSAGE_LENGTH}
            disabled={!connected}
            placeholder={connected ? 'Say Something' : 'Connecting'}
          />
        </label>
        <button type="submit" className="button button-filled" disabled={!connected}>
          Send
        </button>
      </form>
      <p className="meta">Chatting as {guestName}</p>
    </aside>
  )
}
