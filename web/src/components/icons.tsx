import type { ReactNode } from 'react'

type IconProps = { size?: number }

function Svg({ size = 24, children }: IconProps & { children: ReactNode }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
      {children}
    </svg>
  )
}

export function UploadIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M11 16V7.85l-2.6 2.6L7 9l5-5 5 5-1.4 1.45-2.6-2.6V16zm-5 4q-.83 0-1.41-.59Q4 18.83 4 18v-3h2v3h12v-3h2v3q0 .83-.59 1.41Q18.83 20 18 20z" />
    </Svg>
  )
}

export function VideoIcon(props: IconProps) {
  return (
    <Svg {...props}>
      <path d="M4 20q-.83 0-1.41-.59Q2 18.83 2 18V6q0-.83.59-1.41Q3.17 4 4 4h12q.83 0 1.41.59Q18 5.17 18 6v4.5l4-4v11l-4-4V18q0 .83-.59 1.41Q16.83 20 16 20zm0-2h12V6H4zm0 0V6z" />
    </Svg>
  )
}
