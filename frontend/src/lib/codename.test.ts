import { describe, it, expect } from 'vitest'
import { codename, displayName } from './codename'

describe('codename：1v1 会话内的匿名代号（how.md 1.5）', () => {
  it('同一会话内保持稳定', () => {
    const a = codename('conv-1', 1)
    for (let i = 0; i < 5; i++) expect(codename('conv-1', 1)).toBe(a)
  })

  it('格式为 匿名·XXXX（4 位大写十六进制）', () => {
    const a = codename('conv-1', 2)
    expect(a).toMatch(/^匿名·[0-9A-F]{4}$/)
  })

  it('同一会话内不同成员的代号不同', () => {
    expect(codename('conv-1', 1)).not.toBe(codename('conv-1', 2))
  })

  it('同一成员在不同会话之间无法关联', () => {
    const names = new Set<string>()
    for (let i = 0; i < 40; i++) names.add(codename(`conv-${i}`, 1))
    // 40 个会话里出现大量重复才说明可关联（碰撞概率极低）。
    expect(names.size).toBeGreaterThan(30)
  })

  it('自己显示为「我」', () => {
    expect(displayName('conv-1', 1, 1)).toBe('我')
    expect(displayName('conv-1', 1, 2)).toBe(codename('conv-1', 1))
  })

  it('缺少会话 ID 时退化为「匿名」', () => {
    expect(codename('', 1)).toBe('匿名')
  })
})
