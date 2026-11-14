import { describe, it, expect } from 'vitest'
import { render, screen } from '@testing-library/react'
import { WaitingOrb } from './WaitingOrb'

describe('WaitingOrb 涟漪与本人头像', () => {
  it('显示三道错开的涟漪与本人头像，不沿用骰子图标', () => {
    const { container } = render(<WaitingOrb seed="self" nickname="小明" />)
    const rings = screen.getAllByTestId('match-ripple')
    expect(rings).toHaveLength(3)
    expect(rings.map((ring) => ring.style.animationDelay)).toEqual(['0s', '0.8s', '1.6s'])
    expect(rings.every((ring) => ring.classList.contains('animate-match-ripple'))).toBe(true)
    expect(container.querySelector('[data-seed="self"]')).toHaveTextContent('小')
  })
})
