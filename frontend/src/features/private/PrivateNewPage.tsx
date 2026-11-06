import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card } from '../../components/Card'
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

/** 创建私密链接（浅色）。 */
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
    <main className="mx-auto max-w-xl px-4 py-8">
      <h1 className="mb-6 text-2xl font-bold tracking-tight text-lobby-ink">创建私密链接</h1>
      <Card className="space-y-5 p-6">
        {!created ? (
          <>
            <div>
              <label className="mb-1 block text-sm text-lobby-sub">访问密码（可选，4–64 位）</label>
              <input
                aria-label="访问密码"
                className="w-full rounded-sm border border-lobby-line bg-lobby-bg px-3 py-2 text-sm text-lobby-ink"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder="留空表示无需密码"
              />
            </div>
            <div>
              <label className="mb-1 block text-sm text-lobby-sub">有效期</label>
              <select
                aria-label="有效期"
                className="w-full rounded-sm border border-lobby-line bg-lobby-bg px-3 py-2 text-sm text-lobby-ink"
                value={ttl}
                onChange={(e) => setTtl(Number(e.target.value))}
              >
                {TTL_OPTIONS.map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
            </div>
            <label className="flex items-center gap-2 text-sm text-lobby-ink">
              <input
                type="checkbox"
                aria-label="开启阅后即焚"
                className="accent-brand"
                checked={burn}
                onChange={(e) => setBurn(e.target.checked)}
              />
              开启阅后即焚
            </label>
            <button
              type="button"
              className="rounded-sm bg-brand px-5 py-2 text-sm text-white transition duration-150 ease-out disabled:opacity-40"
              disabled={busy}
              onClick={create}
            >
              创建链接
            </button>
          </>
        ) : (
          <>
            <p className="text-sm text-lobby-sub">把这个链接发给对方：</p>
            <div className="flex gap-2">
              <input
                aria-label="私密链接"
                readOnly
                className="flex-1 rounded-sm border border-lobby-line bg-lobby-bg px-3 py-2 text-sm text-lobby-ink"
                value={link}
              />
              <button
                type="button"
                className="rounded-sm bg-lobby-bg px-3 py-1 text-sm text-lobby-ink transition duration-150 ease-out"
                onClick={copy}
              >
                复制
              </button>
            </div>
            <button
              type="button"
              className="rounded-sm bg-brand px-5 py-2 text-sm text-white transition duration-150 ease-out"
              onClick={() => nav(`/p/${created.token}`)}
            >
              进入聊天
            </button>
          </>
        )}
      </Card>
    </main>
  )
}
