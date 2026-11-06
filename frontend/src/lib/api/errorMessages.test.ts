import { describe, it, expect } from 'vitest'
import { errorMessage, KNOWN_ERROR_CODES } from './errorMessages'

describe('errorMessage', () => {
  it('为每个已知错误码返回非空中文提示', () => {
    for (const code of KNOWN_ERROR_CODES) {
      const msg = errorMessage(code)
      expect(msg).toBeTruthy()
      // 至少包含一个中文字符
      expect(/[\u4e00-\u9fa5]/.test(msg)).toBe(true)
    }
  })

  it('包含 how.md 全部错误码与 forbidden_origin', () => {
    const required = [
      'unauthorized', 'verification_required', 'banned', 'rate_limited', 'content_blocked',
      'not_member', 'conversation_ended', 'room_full', 'link_full', 'link_expired',
      'bad_password', 'empty_sea', 'quota_exceeded', 'invalid_request', 'internal',
      'forbidden_origin',
    ]
    for (const code of required) {
      expect(KNOWN_ERROR_CODES).toContain(code)
    }
  })

  it('未知错误码返回通用提示', () => {
    const generic = errorMessage('unknown-body')
    expect(generic).toBe('操作失败，请稍后重试')
    expect(errorMessage(undefined)).toBe(generic)
    expect(errorMessage('')).toBe(generic)
  })
})
