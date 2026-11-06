/** 根据 avatarSeed 本地生成确定性头像图案（5×5 对称，颜色由种子哈希得到）。 */

export interface AvatarPattern {
  /** 25 个格子（5×5），true 表示填充。 */
  cells: boolean[]
  hue: number
}

/** FNV-1a 32 位哈希。 */
export function hashSeed(seed: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < seed.length; i++) {
    h ^= seed.charCodeAt(i)
    h = Math.imul(h, 0x01000193)
  }
  return h >>> 0
}

/** 生成确定性的头像图案：相同种子输出相同，不同种子通常不同。 */
export function generateAvatar(seed: string): AvatarPattern {
  const h = hashSeed(seed)
  const hue = h % 360
  const cells: boolean[] = new Array(25).fill(false)
  // 取左半 3 列，右半镜像，形成左右对称。
  let bits = h
  for (let y = 0; y < 5; y++) {
    for (let x = 0; x < 3; x++) {
      bits = (bits * 1664525 + 1013904223) >>> 0
      const on = (bits & 0x10000) !== 0
      cells[y * 5 + x] = on
      cells[y * 5 + (4 - x)] = on
    }
  }
  return { cells, hue }
}
