import { describe, it, expect, vi, afterEach } from 'vitest'
import { genId, uuidv4 } from './client'

const UUID_V4 = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

describe('genId', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('默认返回 UUID v4', () => {
    expect(genId()).toMatch(UUID_V4)
  })

  it('屏蔽 randomUUID 后仍返回合法的 UUID v4', () => {
    vi.stubGlobal('crypto', {
      getRandomValues: (arr: Uint8Array) => {
        for (let i = 0; i < arr.length; i++) arr[i] = (i * 37 + 11) & 0xff
        return arr
      },
    })
    const id = genId()
    expect(id).toMatch(UUID_V4)
  })

  it('uuidv4 生成互不相同的值', () => {
    const set = new Set<string>()
    for (let i = 0; i < 100; i++) set.add(uuidv4())
    expect(set.size).toBe(100)
  })
})
