import { useRef, useState, type DragEvent, type SubmitEvent } from 'react'
import { errorMessage, uploadVideo } from '../api'
import { formatFileSize } from '../format'
import { UploadIcon } from './icons'

const MAX_TITLE_LENGTH = 200

type Props = {
  onUploaded: () => void
}

export function UploadForm({ onUploaded }: Props) {
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [title, setTitle] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [dragging, setDragging] = useState(false)
  const [progress, setProgress] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const uploading = progress !== null

  // Keeps a picked or dropped file, ready to upload.
  function chooseFile(chosen: File | undefined) {
    if (!chosen) return // This happens when the file picker is cancelled.
    setFile(chosen)
    setError(null)
    // If there's no title yet, suggest the file name without its extension:
    // "holiday.mp4" becomes "holiday". /\.[^.]+$/ matches the last dot and
    // everything after it.
    setTitle((current) => current || chosen.name.replace(/\.[^.]+$/, '').slice(0, MAX_TITLE_LENGTH))
  }

  // Clears the form after a successful upload.
  function reset() {
    setTitle('')
    setFile(null)
    setProgress(null)
    // React doesn't control the file input's value, so clear it directly.
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  // Takes the first file dropped on the drop area.
  function handleDrop(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault() // This stops the browser from opening the file in the tab.
    setDragging(false)
    if (!uploading) chooseFile(event.dataTransfer.files[0])
  }

  // Uploads the file. On success the form clears and the list reloads. On
  // failure the error shows and the form keeps what was entered.
  async function handleSubmit(event: SubmitEvent<HTMLFormElement>) {
    event.preventDefault() // This stops the browser's own submit, which would reload the page.
    if (!file) {
      setError('Choose a Video to Upload.')
      return
    }

    setError(null)
    setProgress(0) // Any number (not null) means an upload is in progress.
    try {
      await uploadVideo(title.trim(), file, setProgress)
      reset()
      onUploaded()
    } catch (err) {
      setError(errorMessage(err))
      setProgress(null)
    }
  }

  return (
    <form className="upload" onSubmit={handleSubmit} aria-label="Upload a Video">
      <label
        className={`dropzone${dragging ? ' dragging' : ''}`}
        onDragOver={(event) => {
          event.preventDefault()
          setDragging(true)
        }}
        onDragLeave={() => setDragging(false)}
        onDrop={handleDrop}
      >
        <input
          ref={fileInputRef}
          type="file"
          accept="video/mp4"
          className="visually-hidden"
          disabled={uploading}
          onChange={(event) => chooseFile(event.target.files?.[0])}
        />
        <span className="dropzone-icon">
          <UploadIcon />
        </span>
        <span className="dropzone-text">
          {file ? (
            <>
              <strong>{file.name}</strong>
              <span className="meta">{formatFileSize(file.size)} · Click to Choose Another</span>
            </>
          ) : (
            <>
              <strong>Drag and Drop a Video, or Click to Browse</strong>
              <span className="meta">MP4 Up to 2 GB</span>
            </>
          )}
        </span>
      </label>

      <div className="upload-row">
        <label className="field">
          <span className="visually-hidden">Title</span>
          <input
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            maxLength={MAX_TITLE_LENGTH}
            required
            disabled={uploading}
            placeholder="Title"
          />
        </label>
        <button type="submit" className="button button-filled" disabled={uploading}>
          {uploading ? `Uploading ${Math.round(progress * 100)}%` : 'Upload'}
        </button>
      </div>

      {uploading && (
        <div
          className="progress"
          role="progressbar"
          aria-label="Upload Progress"
          aria-valuenow={Math.round(progress * 100)}
          aria-valuemin={0}
          aria-valuemax={100}
        >
          <div className="progress-bar" style={{ transform: `scaleX(${progress})` }} />
        </div>
      )}

      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
    </form>
  )
}
