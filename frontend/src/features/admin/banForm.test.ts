import { describe, it, expect } from 'vitest'
import { BAN_DURATIONS, banSubjects, isValidIP, validateBanForm } from './banForm'

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

describe('isValidIP', () => {
  it('接受常见 IPv4 与 IPv6', () => {
    expect(isValidIP('203.0.113.7')).toBe(true)
    expect(isValidIP(' 1.1.1.1 ')).toBe(true)
    expect(isValidIP('2001:db8::1')).toBe(true)
    expect(isValidIP('::1')).toBe(true)
    expect(isValidIP('1:2:3:4:5:6:7:8')).toBe(true)
  })
  it('拒绝非法输入', () => {
    expect(isValidIP('')).toBe(false)
    expect(isValidIP('999.1.1.1')).toBe(false)
    expect(isValidIP('1.1.1')).toBe(false)
    expect(isValidIP('01.1.1.1')).toBe(false)
    expect(isValidIP('example.com')).toBe(false)
    expect(isValidIP('1.1.1.1/24')).toBe(false)
    expect(isValidIP('2001:db8:::1')).toBe(false)
    expect(isValidIP('2001:db8:1')).toBe(false)
  })
})

describe('BAN_DURATIONS', () => {
  it('包含 1 天/7 天/30 天与永久（0 表示永久）', () => {
    expect(BAN_DURATIONS.map((d) => d.value)).toEqual([24, 168, 720, 0])
    expect(BAN_DURATIONS[BAN_DURATIONS.length - 1].label).toBe('永久')
  })
})
