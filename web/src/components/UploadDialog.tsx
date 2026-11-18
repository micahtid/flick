import { useEffect, useRef, useState, type DragEvent, type SubmitEvent } from 'react'
import { MdOutlineFileUpload } from 'react-icons/md'
import { errorMessage, uploadVideo } from '../api'
import { formatFileSize } from '../format'

const MAX_TITLE_LENGTH = 200

type Props = {
  open: boolean
  onClose: () => void
  onUploaded: () => void
}

// Shows the upload form in a dialog over the page. The header's Upload button
// opens it, as does the button shown when there are no videos yet. It closes on Cancel or Escape, except while a file is uploading,
// and closes by itself once the upload has finished.
export function UploadDialog({ open, onClose, onUploaded }: Props) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const [title, setTitle] = useState('')
  const [file, setFile] = useState<File | null>(null)
  const [dragging, setDragging] = useState(false)
  const [progress, setProgress] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const uploading = progress !== null

  // Opens or closes the browser's dialog to match open. showModal() dims the
  // page behind it and keeps the keyboard inside the dialog.
  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    if (open && !dialog.open) dialog.showModal()
    if (!open && dialog.open) dialog.close()
  }, [open])

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

  // Clears the form, so it's empty the next time it opens.
  function reset() {
    setTitle('')
    setFile(null)
    setProgress(null)
    setError(null)
    // React doesn't control the file input's value, so clear it directly.
    if (fileInputRef.current) fileInputRef.current.value = ''
  }

  // Closes the dialog and clears the form, unless a file is uploading.
  function close() {
    if (uploading) return
    reset()
    onClose()
  }

  // Takes the first file dropped on the drop area.
  function handleDrop(event: DragEvent<HTMLLabelElement>) {
    event.preventDefault() // This stops the browser from opening the file in the tab.
    setDragging(false)
    if (!uploading) chooseFile(event.dataTransfer.files[0])
  }

  // Uploads the file. On success the dialog closes and the list reloads. On
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
      onClose()
      onUploaded()
    } catch (err) {
      setError(errorMessage(err))
      setProgress(null)
    }
  }

  return (
    <dialog
      ref={dialogRef}
      className="dialog"
      aria-labelledby="upload-title"
      // The browser fires "cancel" when Escape is pressed. preventDefault
      // stops it closing the dialog itself, so close() can decide.
      onCancel={(event) => {
        event.preventDefault()
        close()
      }}
    >
      <form className="upload" onSubmit={handleSubmit}>
        <h2 id="upload-title" className="dialog-title">
          Upload a Video
        </h2>

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
            <MdOutlineFileUpload size={24} aria-hidden="true" />
          </span>
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
        </label>

        <label className="field">
          <span className="field-label">Title</span>
          <input
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            maxLength={MAX_TITLE_LENGTH}
            required
            disabled={uploading}
            placeholder="Give Your Video a Title"
          />
        </label>

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

        <div className="dialog-actions">
          <button type="button" className="button button-text" onClick={close} disabled={uploading}>
            Cancel
          </button>
          <button type="submit" className="button button-filled" disabled={uploading}>
            {uploading ? `Uploading ${Math.round(progress * 100)}%` : 'Upload'}
          </button>
        </div>
      </form>
    </dialog>
  )
}
