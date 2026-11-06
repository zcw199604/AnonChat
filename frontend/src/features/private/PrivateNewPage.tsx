import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { createPrivateLink } from '../../lib/api/client'
import type { CreatedLink } from '../../lib/api/client'
import { errorMessage } from '../../lib/api/errorMessages'
import { useUiStore } from '../../stores/ui'

const TTL_OPTIONS = [
  { value: 1, label: '1 小时' },
  { value: 24, label: '24 小时' },
  { value: 72, label: '3 天' },
  { value: 168, label: '7 天' },
]

export default function PrivateNewPage() {
  const nav = useNavigate()
  const setToast = useUiStore((s) => s.setToast)
  const [password, setPassword] = useState('')
  const [ttl, setTtl] = useState(24)
  const [burn, setBurn] = useState(false)
  const [created, setCreated] = useState<CreatedLink | null>(null)
  const [busy, setBusy] = useState(false)

  async function create() {
    setBusy(true)
    try {
      const r = await createPrivateLink({ password: password || undefined, ttlHours: ttl, burnAfterRead: burn })
      setCreated(r)
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    } finally {
      setBusy(false)
    }
  }

  const link = created ? `${location.origin}/p/${created.token}` : ''

  async function copy() {
    try {
      await navigator.clipboard.writeText(link)
      setToast('链接已复制')
    } catch {
      setToast('复制失败，请手动复制')
    }
  }

  return (
    <main className="mx-auto max-w-xl px-4 py-6 space-y-4">
      <h1 className="text-2xl font-bold">创建私密链接</h1>
      {!created ? (
        <>
          <div>
            <label className="block text-sm text-slate-400 mb-1">访问密码（可选，4–64 位）</label>
            <input
              aria-label="访问密码"
              className="w-full bg-slate-800 rounded px-3 py-2"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder="留空表示无需密码"
            />
          </div>
          <div>
            <label className="block text-sm text-slate-400 mb-1">有效期</label>
            <select aria-label="有效期" className="w-full bg-slate-800 rounded px-3 py-2" value={ttl} onChange={(e) => setTtl(Number(e.target.value))}>
              {TTL_OPTIONS.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </div>
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={burn} onChange={(e) => setBurn(e.target.checked)} />
            开启阅后即焚
          </label>
          <button type="button" className="px-4 py-2 rounded bg-indigo-600 text-white disabled:opacity-40" disabled={busy} onClick={create}>
            创建链接
          </button>
        </>
      ) : (
        <>
          <p className="text-sm text-slate-400">把这个链接发给对方：</p>
          <div className="flex gap-2">
            <input aria-label="私密链接" readOnly className="flex-1 bg-slate-800 rounded px-3 py-2 text-sm" value={link} />
            <button type="button" className="px-3 py-1 rounded bg-slate-700" onClick={copy}>
              复制
            </button>
          </div>
          <div className="flex gap-2">
            <button type="button" className="px-4 py-2 rounded bg-indigo-600 text-white" onClick={() => nav(`/p/${created.token}`)}>
              进入聊天
            </button>
          </div>
        </>
      )}
    </main>
  )
}
