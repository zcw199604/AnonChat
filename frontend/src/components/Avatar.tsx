import { generateAvatar } from './avatarPattern'

interface AvatarProps {
  seed: string
  size?: number
  className?: string
}

/** 根据 avatarSeed 本地渲染一个确定性的 SVG 头像（不访问外部服务）。 */
export function Avatar({ seed, size = 40, className }: AvatarProps) {
  const { cells, hue } = generateAvatar(seed)
  const color = `hsl(${hue} 70% 55%)`
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 5 5"
      role="img"
      aria-label="头像"
      className={`rounded-full shrink-0 ${className ?? ''}`}
      data-seed={seed}
    >
      <rect width={5} height={5} fill="#1e293b" />
      {cells.map((on, i) =>
        on ? (
          <rect key={i} x={i % 5} y={Math.floor(i / 5)} width={1} height={1} fill={color} />
        ) : null,
      )}
    </svg>
  )
}
