import { describe, it, expect } from 'vitest'
import { generateAvatar } from './avatarPattern'

describe('generateAvatar', () => {
  it('相同种子输出相同', () => {
    expect(generateAvatar('abc')).toEqual(generateAvatar('abc'))
  })

  it('不同种子输出不同', () => {
    const a = generateAvatar('seed-one')
    const b = generateAvatar('seed-two')
    expect(a).not.toEqual(b)
  })

  it('产生 25 个格子且左右对称', () => {
    const { cells } = generateAvatar('symmetry')
    expect(cells).toHaveLength(25)
    for (let y = 0; y < 5; y++) {
      for (let x = 0; x < 3; x++) {
        expect(cells[y * 5 + x]).toBe(cells[y * 5 + (4 - x)])
      }
    }
  })
})
