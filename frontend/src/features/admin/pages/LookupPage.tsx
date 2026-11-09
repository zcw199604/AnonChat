import { useCallback, useEffect, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { adminLookupIdentity } from '../../../lib/api/client'
import type { AdminIdentityLookup } from '../../../lib/api/client'
import { useAdmin } from '../context'
import { formatTime } from '../labels'
import { Button, EmptyState, Field, PageHeader, Panel, StatusBadge, inputClass } from '../ui'

/** 身份查询：回车即查，结果写入 ?code= 便于分享/刷新保留。 */
export function LookupPage() {
  const { onError } = useAdmin()
  const [params, setParams] = useSearchParams()
  const codeParam = params.get('code') ?? ''

  const [input, setInput] = useState(codeParam)
  const [result, setResult] = useState<AdminIdentityLookup | null>(null)
  const [busy, setBusy] = useState(false)
  const [searched, setSearched] = useState('')

  const runLookup = useCallback(
    async (raw: string) => {
      const code = raw.trim().toUpperCase()
      if (code === '') return
      setBusy(true)
      setResult(null)
      setSearched(code)
      try {
        setResult(await adminLookupIdentity(code))
      } catch (e) {
        onError(e)
      } finally {
        setBusy(false)
      }
    },
    [onError],
  )

  // 首次进入时若 URL 带 ?code=，自动查询一次（之后由用户手动触发）。
  const autoRan = useRef(false)
  useEffect(() => {
    if (autoRan.current) return
    autoRan.current = true
    void (async () => {
      await runLookup(codeParam)
    })()
  }, [codeParam, runLookup])

  function submit(e: React.FormEvent) {
    e.preventDefault()
    const code = input.trim().toUpperCase()
    if (code === '') return
    setInput(code)
    setParams({ code }, { replace: true })
    void runLookup(code)
  }

  return (
    <div>
      <PageHeader
        title="身份查询"
        description="输入匿名编号（例如 ABCD3FGH9K）查询该身份的实名与封禁状态。"
      />

      <Panel className="p-4">
        <form className="flex flex-wrap items-end gap-2" onSubmit={submit}>
          <div className="min-w-[220px] flex-1">
            <Field label="匿名编号">
              <input
                aria-label="匿名编号"
                className={`${inputClass} font-mono uppercase`}
                placeholder="例如 ABCD3FGH9K"
                value={input}
                onChange={(e) => setInput(e.target.value)}
              />
            </Field>
          </div>
          <Button type="submit" variant="primary" loading={busy} disabled={input.trim() === ''}>
            查询
          </Button>
        </form>
      </Panel>

      {busy && <p className="mt-4 text-sm text-lobby-sub">查询中…</p>}

      {!busy && result && (
        <Panel className="mt-4 p-4">
          <div className="mb-3 flex flex-wrap items-center gap-2">
            <h2 className="text-sm font-semibold text-lobby-ink">{result.summary.nickname || '未设置昵称'}</h2>
            <StatusBadge tone={result.vipActive ? 'success' : 'neutral'}>
              {result.vipActive ? 'VIP 生效中' : '未开通 VIP'}
            </StatusBadge>
            <StatusBadge tone={result.banned ? 'danger' : 'success'}>
              {result.banned ? '已封禁' : '状态正常'}
            </StatusBadge>
          </div>
          <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-2">
            <div>
              <dt className="text-xs text-lobby-sub">身份 ID</dt>
              <dd className="font-mono text-lobby-ink select-all">{result.identityId}</dd>
            </div>
            <div>
              <dt className="text-xs text-lobby-sub">匿名编号</dt>
              <dd className="font-mono text-lobby-ink select-all">{result.publicCode}</dd>
            </div>
            <div>
              <dt className="text-xs text-lobby-sub">创建时间</dt>
              <dd className="text-lobby-ink">{formatTime(result.createdAt)}</dd>
            </div>
            <div>
              <dt className="text-xs text-lobby-sub">历史被举报次数</dt>
              <dd className="text-lobby-ink">{result.summary.reportCount} 次</dd>
            </div>
            <div>
              <dt className="text-xs text-lobby-sub">创建 IP</dt>
              <dd className="font-mono text-lobby-ink">{result.summary.createdIp || '—'}</dd>
            </div>
            <div>
              <dt className="text-xs text-lobby-sub">最近连接 IP</dt>
              <dd className="font-mono text-lobby-ink">{result.summary.lastIp || '—'}</dd>
            </div>
            <div>
              <dt className="text-xs text-lobby-sub">实名号码</dt>
              <dd className="font-mono text-lobby-ink">{result.summary.phoneMasked || '未实名'}</dd>
            </div>
          </dl>
        </Panel>
      )}

      {!busy && !result && searched !== '' && (
        <div className="mt-4">
          <EmptyState>没有查到编号 {searched} 对应的身份</EmptyState>
        </div>
      )}
    </div>
  )
}
