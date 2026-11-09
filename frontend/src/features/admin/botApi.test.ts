import { afterEach, describe, expect, it, vi } from 'vitest'
import * as api from './botApi'

afterEach(() => vi.unstubAllGlobals())

describe('后台人设与记忆 API', () => {
  it('导入与表情上传使用 file multipart，并交由浏览器生成 boundary', async () => {
    const fetcher = vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ card: { id:'c1' }, warnings:['提示'] }), { status:201 })))
    vi.stubGlobal('fetch', fetcher)
    const file = new File(['{}'], 'card.json')
    await expect(api.adminImportCharacterCard(file)).resolves.toEqual({ card:{ id:'c1' }, warnings:['提示'] })
    expect(fetcher).toHaveBeenLastCalledWith('/api/admin/character-cards/import', expect.objectContaining({ method:'POST', credentials:'same-origin' }))
    let req = fetcher.mock.calls.at(-1)![1]
    expect(req.body.get('file')).toBe(file)
    expect(req.headers).toBeUndefined()
    await api.adminCreateCardExpression('c1', file, 'happy', '开心')
    req = fetcher.mock.calls.at(-1)![1]
    expect(fetcher.mock.calls.at(-1)![0]).toBe('/api/admin/character-cards/c1/expressions')
    expect(req.body.get('key')).toBe('happy'); expect(req.body.get('description')).toBe('开心'); expect(req.body.get('file')).toBe(file)
    expect(req.headers).toBeUndefined()
  })

  it('查询、修改、删除表情和分页记忆严格对应后台路径与字段', async () => {
    const fetcher=vi.fn().mockImplementation(()=>Promise.resolve(new Response('[]',{status:200})))
    vi.stubGlobal('fetch',fetcher)
    await api.adminCardExpressions('c1')
    expect(fetcher.mock.calls.at(-1)![0]).toBe('/api/admin/character-cards/c1/expressions')
    await api.adminUpdateCardExpression('e1',{key:'happy',description:'开心',sort:2})
    expect(fetcher.mock.calls.at(-1)).toEqual(['/api/admin/card-expressions/e1',expect.objectContaining({method:'PATCH',body:JSON.stringify({key:'happy',description:'开心',sort:2})})])
    await api.adminDeleteCardExpression('e1')
    expect(fetcher.mock.calls.at(-1)).toEqual(['/api/admin/card-expressions/e1',expect.objectContaining({method:'DELETE'})])
    await api.adminBotMemories('b1',20,40)
    expect(fetcher.mock.calls.at(-1)![0]).toBe('/api/admin/bots/b1/memories?limit=20&offset=40')
    await api.adminBotMemory('b1','p1')
    expect(fetcher.mock.calls.at(-1)![0]).toBe('/api/admin/bots/b1/memories/p1')
    await api.adminDeleteBotMemory('b1','p1')
    expect(fetcher.mock.calls.at(-1)).toEqual(['/api/admin/bots/b1/memories/p1',expect.objectContaining({method:'DELETE'})])
    await api.adminClearBotMemories('b1')
    expect(fetcher.mock.calls.at(-1)).toEqual(['/api/admin/bots/b1/memories',expect.objectContaining({method:'DELETE'})])
  })

  it('上传接口保留后端中文错误供现有错误提示展示', async () => {
    vi.stubGlobal('fetch',vi.fn().mockResolvedValue(new Response(JSON.stringify({error:{code:'invalid_request',message:'上传文件超过大小上限'}}),{status:400})))
    await expect(api.adminImportCharacterCard(new File(['x'],'bad.krp'))).rejects.toThrow('上传文件超过大小上限')
  })
})
