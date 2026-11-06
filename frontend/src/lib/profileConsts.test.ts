import { describe, it, expect } from 'vitest'
import { MAX_AGE, MAX_NICKNAME, MAX_TAGS, MIN_AGE, MIN_NICKNAME, PROVINCES, TAGS } from './profileConsts'

describe('资料常量（how.md 3.2）', () => {
  it('省份为 34 项且无重复', () => {
    expect(PROVINCES).toHaveLength(34)
    expect(new Set(PROVINCES).size).toBe(34)
    expect(PROVINCES[0]).toBe('北京')
    expect(PROVINCES[PROVINCES.length - 1]).toBe('台湾')
  })

  it('标签为 18 项且无重复', () => {
    expect(TAGS).toHaveLength(18)
    expect(new Set(TAGS).size).toBe(18)
    expect(TAGS).toContain('音乐')
    expect(TAGS).toContain('二次元')
  })

  it('边界值与后端一致', () => {
    expect(MAX_TAGS).toBe(5)
    expect(MIN_AGE).toBe(18)
    expect(MAX_AGE).toBe(80)
    expect(MIN_NICKNAME).toBe(2)
    expect(MAX_NICKNAME).toBe(12)
  })
})
