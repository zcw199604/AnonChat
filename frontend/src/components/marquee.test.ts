import { describe, it, expect } from 'vitest'
import { marqueeNeedsScroll } from './marqueeScroll'

describe('marqueeNeedsScroll：公告跑马灯是否需要滚动（how.md 1.4）', () => {
  it('只有一条且放得下时不滚动', () => {
    expect(marqueeNeedsScroll(1, 300, 300)).toBe(false)
    expect(marqueeNeedsScroll(1, 200, 300)).toBe(false)
  })

  it('内容超出容器时滚动', () => {
    expect(marqueeNeedsScroll(1, 400, 300)).toBe(true)
    expect(marqueeNeedsScroll(3, 900, 300)).toBe(true)
  })

  it('容器宽度未知（未测量）时不滚动，避免闪烁', () => {
    expect(marqueeNeedsScroll(2, 0, 0)).toBe(false)
  })
})
