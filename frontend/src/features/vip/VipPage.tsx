import { useCallback, useEffect, useState } from 'react'
import { Card } from '../../components/Card'
import { useIdentityStore } from '../../stores/identity'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { myBroadcasts, postBroadcast, redeemVip } from '../../lib/api/client'
import type { Broadcast } from '../../lib/api/client'

const STATUS_TEXT: Record<string, string> = {
  pending: '待审核',
  approved: '已展示',
  rejected: '已驳回',
  removed: '已下架',
}

/** VIP 与激活码（how.md 2.4）：状态、权益、兑换、剩余广播卡、发布广播、我的广播。 */
export default function VipPage() {
  const me = useIdentityStore((s) => s.me)
  const refreshMe = useIdentityStore((s) => s.refreshMe)
  const setToast = useUiStore((s) => s.setToast)

  const [code, setCode] = useState('')
  const [busy, setBusy] = useState(false)
  const [content, setContent] = useState('')
  const [mine, setMine] = useState<Broadcast[]>([])

  const loadMine = useCallback(() => {
    myBroadcasts()
      .then(setMine)
      .catch(() => undefined)
  }, [])

  useEffect(() => {
    loadMine()
  }, [loadMine])

  const vipActive = Boolean(me?.vip.active)
  const cards = me?.broadcastCards ?? 0

  async function doRedeem() {
    if (code.trim() === '') return
    setBusy(true)
    try {
      await redeemVip(code.trim())
      await refreshMe()
      setCode('')
      setToast('兑换成功，VIP 特权已生效')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    } finally {
      setBusy(false)
    }
  }

  async function doPublish() {
    if (content.trim() === '') return
    setBusy(true)
    try {
      await postBroadcast(content.trim())
      setContent('')
      await refreshMe()
      loadMine()
      setToast('广播已提交，等待审核')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="mx-auto max-w-3xl space-y-5 px-4 py-8">
      <h1 className="text-2xl font-bold tracking-tight text-lobby-ink">VIP 与激活码</h1>

      <Card className="space-y-2 p-6">
        <div className="text-sm text-lobby-sub">当前状态</div>
        <div className="text-lg font-semibold text-lobby-ink">
          {vipActive ? `VIP 生效中` : '普通用户'}
        </div>
        {vipActive && me?.vip.until && (
          <div className="text-sm text-lobby-sub">有效期至 {me.vip.until.slice(0, 10)}</div>
        )}
        <div className="text-sm text-lobby-sub">剩余广播卡：{cards} 张</div>
      </Card>

      <Card className="space-y-3 p-6">
        <h2 className="text-sm font-semibold text-lobby-ink">VIP 权益</h2>
        <ul className="space-y-1 text-sm text-lobby-sub">
          <li>· 按性别筛选匹配对象（只匹配男生 / 只匹配女生）</li>
          <li>· 同省匹配（只和同省的陌生人配对）</li>
          <li>· 兑换后附带广播卡，可发布一条 24 小时广播</li>
        </ul>
        <p className="text-xs text-lobby-sub">站内不提供任何支付，激活码由运营方在站外分发。</p>
      </Card>

      <Card className="space-y-3 p-6">
        <h2 className="text-sm font-semibold text-lobby-ink">兑换激活码</h2>
        <div className="flex gap-2">
          <input
            aria-label="激活码"
            className="flex-1 rounded-sm border border-lobby-line bg-lobby-bg px-3 py-2 font-mono text-sm text-lobby-ink"
            placeholder="XXXX-XXXX-XXXX-XXXX"
            value={code}
            onChange={(e) => setCode(e.target.value)}
          />
          <button
            type="button"
            className="rounded-sm bg-brand px-4 py-2 text-sm text-white transition duration-150 ease-out disabled:opacity-40"
            disabled={busy || code.trim() === ''}
            onClick={doRedeem}
          >
            兑换
          </button>
        </div>
      </Card>

      <Card className="space-y-3 p-6">
        <h2 className="text-sm font-semibold text-lobby-ink">发布广播</h2>
        <p className="text-xs text-lobby-sub">
          消耗 1 张广播卡。提交后需要人工审核，通过后在首页展示 24 小时；被驳回会退还广播卡。
        </p>
        <textarea
          aria-label="广播内容"
          className="w-full resize-none rounded-sm border border-lobby-line bg-lobby-bg p-3 text-sm text-lobby-ink"
          rows={3}
          maxLength={60}
          value={content}
          onChange={(e) => setContent(e.target.value)}
          placeholder="想说的话，最多 60 字"
        />
        <div className="flex items-center justify-between">
          <span className="text-xs text-lobby-sub">{[...content].length} / 60</span>
          <button
            type="button"
            className="rounded-sm bg-brand px-4 py-2 text-sm text-white transition duration-150 ease-out disabled:opacity-40"
            disabled={busy || cards === 0 || content.trim() === ''}
            onClick={doPublish}
          >
            提交审核
          </button>
        </div>
        {cards === 0 && <p className="text-xs text-lobby-sub">没有可用的广播卡，兑换激活码即可获得。</p>}
      </Card>

      <Card className="space-y-3 p-6">
        <h2 className="text-sm font-semibold text-lobby-ink">我的广播</h2>
        {mine.length === 0 && <p className="text-sm text-lobby-sub">还没有提交过广播。</p>}
        <ul className="space-y-2">
          {mine.map((b) => (
            <li key={b.id} className="rounded-sm border border-lobby-line p-3 text-sm">
              <div className="text-lobby-ink">{b.content}</div>
              <div className="mt-1 text-xs text-lobby-sub">
                {STATUS_TEXT[b.status] ?? b.status}
                {b.rejectReason ? ` · 原因：${b.rejectReason}` : ''}
              </div>
            </li>
          ))}
        </ul>
      </Card>
    </main>
  )
}
