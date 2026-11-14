import type { ReactNode } from 'react'

const circle = <circle cx="12" cy="12" r="9" />
const paths = {
  back: <path d="M15 18l-6-6 6-6" />,
  more: <>{[5, 12, 19].map((cx) => <circle key={cx} cx={cx} cy="12" r="1.8" fill="currentColor" stroke="none" />)}</>,
  shuffle: <path d="M16 3h5v5 M4 20L21 3 M21 16v5h-5 M15 15l6 6 M4 4l5 5" />,
  plus: <path d="M12 5v14 M5 12h14" />,
  smile: <>{circle}<path d="M8.5 14.5s1.3 1.8 3.5 1.8 3.5-1.8 3.5-1.8 M9 9.5h.01 M15 9.5h.01" /></>,
  send: <path d="M12 19V5 M5 12l7-7 7 7" />,
  flag: <path d="M5 21V4 M5 4h12l-2.5 4.5L17 13H5" />,
  ban: <>{circle}<path d="M5.6 5.6l12.8 12.8" /></>,
  close: <path d="M18 6L6 18 M6 6l12 12" />,
  'arrow-down': <path d="M12 5v14 M19 12l-7 7-7-7" />,
  alert: <>{circle}<path d="M12 8v4.5 M12 16h.01" /></>,
  sparkle: <path d="M12 3l1.9 5.6 5.6 1.9-5.6 1.9L12 18l-1.9-5.6L4.5 10.5l5.6-1.9z" />,
} satisfies Record<string, ReactNode>

interface IconProps {
  name: keyof typeof paths
  size?: number
  strokeWidth?: number
  className?: string
}

export function Icon({ name, size = 24, strokeWidth = 1.8, className }: IconProps) {
  return (
    <svg aria-hidden="true" width={size} height={size} viewBox="0 0 24 24" fill="none"
      stroke="currentColor" strokeWidth={strokeWidth} strokeLinecap="round" strokeLinejoin="round"
      className={className}>
      {paths[name]}
    </svg>
  )
}
