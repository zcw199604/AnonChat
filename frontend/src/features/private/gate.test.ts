import { describe, it, expect } from 'vitest'
import { privateGate } from './gate'

describe('privateGate', () => {
  it('已销毁/过期/已满优先', () => {
    expect(privateGate('destroyed', false)).toBe('destroyed')
    expect(privateGate('expired', true)).toBe('expired')
    expect(privateGate('full', false)).toBe('full')
  })
  it('活跃链接按是否需要密码分流', () => {
    expect(privateGate('active', true)).toBe('password')
    expect(privateGate('active', false)).toBe('join')
  })
})
