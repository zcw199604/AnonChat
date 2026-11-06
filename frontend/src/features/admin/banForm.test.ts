import { describe, it, expect } from 'vitest'
import { validateBanForm, banSubjects } from './banForm'

const base = { identity: false, ip: false, account: false, durationHours: null, reason: '' }

describe('validateBanForm', () => {
  it('未选任何对象且无原因时报错', () => {
    const r = validateBanForm(base)
    expect(r.ok).toBe(false)
    expect(r.errors).toContain('请至少选择一个封禁对象')
    expect(r.errors).toContain('请填写封禁原因')
  })
  it('选了对象且填写原因则通过', () => {
    const r = validateBanForm({ ...base, identity: true, reason: 'spam' })
    expect(r.ok).toBe(true)
  })
  it('只缺原因仍报错', () => {
    const r = validateBanForm({ ...base, ip: true, reason: '   ' })
    expect(r.ok).toBe(false)
    expect(r.errors).toEqual(['请填写封禁原因'])
  })
})

describe('banSubjects', () => {
  it('按勾选生成类型列表', () => {
    expect(banSubjects({ ...base, identity: true, account: true })).toEqual(['identity', 'account'])
  })
})
