import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { WaitingOrb } from './WaitingOrb'

/**
 * 匹配等待态的大渐变圆外圈必须有呼吸光环（how.md 2.3，审查意见 V3）。
 * 动画用 animate-breathe（1.6s，≥1.6s 的慢速 scale+opacity）；
 * prefers-reduced-motion 下的关闭由 index.css 的全局规则负责。
 */
describe('WaitingOrb：等待态呼吸光环', () => {
  it('大圆外有一圈 brand/20 的光环，并使用 animate-breathe', () => {
    render(<WaitingOrb />)

    const halo = screen.getByTestId('match-halo')
    expect(halo.className).toContain('animate-breathe')
    // 光环颜色来自品牌紫的低透明度（brand/20），不引入第三种强调色。
    expect(halo.getAttribute('style') ?? '').toMatch(/--color-brand/)
    expect(halo.getAttribute('style') ?? '').toContain('20%')

    // 光环在大圆之外：光环尺寸大于渐变圆。
    const orb = screen.getByTestId('match-orb')
    expect(halo.className).toContain('h-40')
    expect(orb.className).toContain('h-32')
  })
})
