import { hashSeed } from './avatarPattern'

/** 品牌紫与辅助粉的色相区间（how.md 1.4：渐变只在这两种强调色之间取值）。 */
const HUE_BRAND = 245 // #8B7CF6
const HUE_ACCENT = 325 // #E88BC4

interface GradientAvatarProps {
  seed: string
  size?: number
  /** 中心显示的一个字（大厅资料卡显示昵称首字）。 */
  label?: string
  /** 中心显示的 emoji（与 label 二选一）。 */
  emoji?: string
  className?: string
}

/**
 * 渐变头像：角度和两个色标的明度由 seed 的哈希决定，色相始终限制在
 * 品牌紫 → 辅助粉之间。不使用像素图案、不使用真人头像或外链图片。
 */
export function GradientAvatar({ seed, size = 40, label, emoji, className }: GradientAvatarProps) {
  const h = hashSeed(seed || 'anonymous')
  const angle = 90 + (h % 180)
  const hueA = HUE_BRAND + ((h >>> 3) % (HUE_ACCENT - HUE_BRAND + 1))
  const hueB = HUE_BRAND + ((h >>> 11) % (HUE_ACCENT - HUE_BRAND + 1))
  const lightA = 62 + ((h >>> 5) % 10)
  const lightB = 68 + ((h >>> 13) % 10)

  const style = {
    width: size,
    height: size,
    background: `linear-gradient(${angle}deg, hsl(${hueA} 84% ${lightA}%), hsl(${hueB} 86% ${lightB}%))`,
    fontSize: Math.round(size * 0.42),
  }

  return (
    <span
      className={`inline-flex shrink-0 select-none items-center justify-center rounded-full font-semibold text-white ${className ?? ''}`}
      style={style}
      role="img"
      aria-label="头像"
      data-seed={seed}
    >
      {emoji ?? (label ? [...label][0] : '')}
    </span>
  )
}
