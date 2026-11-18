import type { MouseEvent, ReactNode } from 'react'
import { navigate } from '../router'

type LinkProps = {
  href: string
  className?: string
  'aria-label'?: string
  'aria-current'?: 'page'
  children: ReactNode
}

export function Link({ href, children, ...rest }: LinkProps) {
  // Switches page without reloading the website.
  function handleClick(event: MouseEvent<HTMLAnchorElement>) {
    // Leave Ctrl/Cmd/Shift/Alt clicks and non-left clicks (button 0 is the
    // left button) to the browser, so "open in new tab" still works.
    const modified = event.metaKey || event.ctrlKey || event.shiftKey || event.altKey
    if (modified || event.button !== 0) return
    event.preventDefault() // This stops the browser from loading the page from scratch.
    navigate(href)
  }

  return (
    <a href={href} onClick={handleClick} {...rest}>
      {children}
    </a>
  )
}
