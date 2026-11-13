import { afterEach, expect, it, vi } from 'vitest'
import * as api from './client'

afterEach(() => vi.unstubAllGlobals())

it('上传媒体用同源 Cookie 的 FormData，不设置 JSON Content-Type', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ mediaId: 'm1', kind: 'image' })))
  vi.stubGlobal('fetch', fetch)
  const file = new File(['png'], 'photo.png', { type: 'image/png' })
  await expect(api.uploadMedia(file)).resolves.toMatchObject({ mediaId: 'm1', kind: 'image' })
  const [url, init] = fetch.mock.calls[0]
  expect(url).toBe('/api/media/upload')
  expect(init).toMatchObject({ method: 'POST', credentials: 'same-origin' })
  expect(new Headers(init.headers).has('Content-Type')).toBe(false)
  expect((init.body as FormData).get('file')).toBe(file)
})

it('上传失败保留后端错误码与文案', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { code: 'media_rejected', message: '文件超过大小上限' } }), { status: 400 })))
  await expect(api.uploadMedia(new File(['x'], 'x.png'))).rejects.toMatchObject({ code: 'media_rejected', message: '文件超过大小上限' })
})
