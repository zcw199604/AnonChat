import { useCallback, useState } from 'react'
import {
  adminActivationBatches,
  adminCreateActivationBatch,
  adminRevokeActivationBatch,
} from '../../../lib/api/client'
import type { ActivationBatch } from '../../../lib/api/client'
import { useUiStore } from '../../../stores/ui'
import { useAdmin } from '../context'
import { useAsyncData } from '../hooks'
import { formatTime } from '../labels'
import {
  Button,
  ConfirmDialog,
  EmptyState,
  Field,
  LoadingRows,
  PageHeader,
  Panel,
  inputClass,
} from '../ui'

const DEFAULT_FORM = { label: '', vipDays: 30, broadcastCards: 3, count: 10, days: 30 }

/** 激活码：生成批次（含有效期）、一次性明文展示、撤销未兑换。 */
export function CodesPage() {
  const { onError } = useAdmin()
  const setToast = useUiStore((s) => s.setToast)
  const [form, setForm] = useState(DEFAULT_FORM)
  const [creating, setCreating] = useState(false)
  /** 明文激活码只在创建后展示一次。 */
  const [codes, setCodes] = useState<string[]>([])
  const [revokeTarget, setRevokeTarget] = useState<ActivationBatch | null>(null)

  const load = useCallback(async () => (await adminActivationBatches()).batches, [])
  const { data, loading, reload } = useAsyncData<ActivationBatch[]>('batches', load, onError)
  const batches = data ?? []

  const canCreate = form.count > 0 && !creating

  async function create() {
    if (!canCreate) return
    setCreating(true)
    try {
      const expiresAt = new Date(Date.now() + form.days * 24 * 3600 * 1000).toISOString()
      const r = await adminCreateActivationBatch({
        label: form.label.trim() || '未命名批次',
        vipDays: form.vipDays,
        broadcastCards: form.broadcastCards,
        count: form.count,
        expiresAt,
      })
      setCodes(r.codes)
      setToast('批次已创建，请立即下载明文激活码')
      reload()
    } catch (e) {
      onError(e)
    } finally {
      setCreating(false)
    }
  }

  async function revoke() {
    if (!revokeTarget) return
    try {
      const r = await adminRevokeActivationBatch(revokeTarget.id)
      setToast(`已撤销 ${r.revoked} 个未兑换的激活码`)
      setRevokeTarget(null)
      reload()
    } catch (e) {
      onError(e)
    }
  }

  /** CSV 下载：明文只在创建时出现一次，由前端直接生成本地文件。 */
  function downloadCSV() {
    const csv = `code\n${codes.join('\n')}\n`
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `activation-codes-${Date.now()}.csv`
    a.click()
    URL.revokeObjectURL(url)
  }

  async function copyAll() {
    try {
      await navigator.clipboard.writeText(codes.join('\n'))
      setToast('已复制全部激活码')
    } catch {
      setToast('复制失败，请手动选择文本复制')
    }
  }

  return (
    <div>
      <PageHeader title="激活码" description="批量生成 VIP 激活码，明文仅在创建后展示一次。" />

      <Panel className="p-4">
        <h2 className="mb-3 text-sm font-semibold text-lobby-ink">生成批次</h2>
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          <Field label="批次名称">
            <input
              aria-label="批次名称"
              className={inputClass}
              placeholder="例如 三月推广"
              value={form.label}
              onChange={(e) => setForm({ ...form, label: e.target.value })}
            />
          </Field>
          <Field label="VIP 天数">
            <input
              aria-label="VIP 天数"
              type="number"
              min={0}
              className={inputClass}
              value={form.vipDays}
              onChange={(e) => setForm({ ...form, vipDays: Number(e.target.value) })}
            />
          </Field>
          <Field label="广播卡张数">
            <input
              aria-label="广播卡张数"
              type="number"
              min={0}
              className={inputClass}
              value={form.broadcastCards}
              onChange={(e) => setForm({ ...form, broadcastCards: Number(e.target.value) })}
            />
          </Field>
          <Field label="激活码数量">
            <input
              aria-label="激活码数量"
              type="number"
              min={1}
              className={inputClass}
              value={form.count}
              onChange={(e) => setForm({ ...form, count: Number(e.target.value) })}
            />
          </Field>
          <Field label="有效期（天）" hint="从生成时刻起算，到期后激活码失效。">
            <input
              aria-label="有效期（天）"
              type="number"
              min={1}
              className={inputClass}
              value={form.days}
              onChange={(e) => setForm({ ...form, days: Number(e.target.value) })}
            />
          </Field>
        </div>
        <div className="mt-3 flex justify-end">
          <Button variant="primary" loading={creating} disabled={!canCreate} onClick={() => void create()}>
            生成批次
          </Button>
        </div>
      </Panel>

      {codes.length > 0 && (
        <Panel className="mt-4 space-y-2 border-lobby-ink/20 p-4">
          <p className="text-xs font-medium text-danger">明文激活码只显示这一次，请立即下载或复制。</p>
          <div className="flex flex-wrap gap-2">
            <Button variant="primary" size="sm" onClick={downloadCSV}>
              下载 CSV
            </Button>
            <Button variant="secondary" size="sm" onClick={() => void copyAll()}>
              复制全部
            </Button>
            <Button variant="ghost" size="sm" onClick={() => setCodes([])}>
              我已保存
            </Button>
          </div>
          <div className="max-h-40 overflow-y-auto font-mono text-xs text-lobby-ink">
            {codes.map((c) => (
              <div key={c}>{c}</div>
            ))}
          </div>
        </Panel>
      )}

      <h2 className="mt-6 mb-2 text-sm font-medium text-lobby-sub">批次列表</h2>
      {loading ? (
        <LoadingRows rows={3} />
      ) : batches.length === 0 ? (
        <EmptyState>暂无批次</EmptyState>
      ) : (
        <Panel className="overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[820px] text-left text-sm">
              <thead className="border-b border-lobby-line text-xs text-lobby-sub">
                <tr>
                  <th className="px-4 py-2 font-medium">名称</th>
                  <th className="px-4 py-2 font-medium">VIP 天数</th>
                  <th className="px-4 py-2 font-medium">广播卡</th>
                  <th className="px-4 py-2 font-medium">总数</th>
                  <th className="px-4 py-2 font-medium">已兑换</th>
                  <th className="px-4 py-2 font-medium">已撤销</th>
                  <th className="px-4 py-2 font-medium">有效期至</th>
                  <th className="px-4 py-2 text-right font-medium">操作</th>
                </tr>
              </thead>
              <tbody>
                {batches.map((b) => {
                  const progress = b.total > 0 ? Math.round((b.redeemed / b.total) * 100) : 0
                  return (
                    <tr key={b.id} className="border-b border-lobby-line/60 last:border-0">
                      <td className="px-4 py-2.5 text-lobby-ink">{b.label}</td>
                      <td className="px-4 py-2.5 tabular-nums">{b.vipDays}</td>
                      <td className="px-4 py-2.5 tabular-nums">{b.broadcastCards}</td>
                      <td className="px-4 py-2.5 tabular-nums">{b.total}</td>
                      <td className="px-4 py-2.5 tabular-nums">
                        {b.redeemed}
                        <span className="ml-2 hidden h-1 w-16 overflow-hidden rounded-full bg-lobby-line align-middle sm:inline-block">
                          <span
                            className="block h-full rounded-full bg-brand"
                            style={{ width: `${progress}%` }}
                          />
                        </span>
                      </td>
                      <td className="px-4 py-2.5 tabular-nums">{b.revoked}</td>
                      <td className="px-4 py-2.5 text-xs text-lobby-sub">{formatTime(b.expiresAt)}</td>
                      <td className="px-4 py-2.5 text-right">
                        <Button variant="danger" size="sm" onClick={() => setRevokeTarget(b)}>
                          撤销未兑换
                        </Button>
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        </Panel>
      )}

      <ConfirmDialog
        open={revokeTarget !== null}
        title="撤销该批次未兑换的激活码？"
        description={
          revokeTarget
            ? `批次「${revokeTarget.label}」中尚未兑换的激活码会立即失效，已兑换的不受影响。`
            : undefined
        }
        confirmText="撤销未兑换"
        danger
        onConfirm={revoke}
        onClose={() => setRevokeTarget(null)}
      />
    </div>
  )
}
