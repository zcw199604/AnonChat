import { useEffect, useState } from 'react'
import { adminRateLimitRules, adminUpdateRateLimitRules } from '../../../lib/api/client'
import type { AdminRateLimitRule, AdminRateLimitSettings } from '../../../lib/api/client'
import { useUiStore } from '../../../stores/ui'

const GROUPS = { identity: '身份', message: '消息', match: '匹配', ws: '连接', other: '其他' }
const UNITS = [[1, '秒'], [60, '分'], [3600, '时'], [86400, '天']] as const
const PRESETS = [0, 3600, 86400, 604800, -1]
const cls = 'w-24 rounded-sm border border-lobby-line bg-lobby-card px-2 py-1 text-lobby-ink'

function WindowInput({ rule, change }: { rule: AdminRateLimitRule; change: (n: number) => void }) {
  const [unit, setUnit] = useState<number>(() => [...UNITS].reverse().find(([n]) => rule.windowSeconds % n === 0)?.[0] ?? 1)
  return <div className="flex gap-1">
    <input aria-label={`${rule.name}时间窗口`} className={cls} type="number" step="any" min={rule.minWindowSeconds / unit} max={rule.maxWindowSeconds / unit} value={rule.windowSeconds / unit} onChange={(e) => change(Number(e.target.value) * unit)} />
    <select aria-label={`${rule.name}时间单位`} className={cls} value={unit} onChange={(e) => setUnit(Number(e.target.value))}>{UNITS.map(([n, label]) => <option key={n} value={n}>{label}</option>)}</select>
  </div>
}

function BanInput({ rule, change }: { rule: AdminRateLimitRule; change: (n: number) => void }) {
  const [custom, setCustom] = useState(!PRESETS.includes(rule.banSeconds))
  if (!rule.banSubject) return <select aria-label={`${rule.name}超限封禁`} disabled className={cls}><option>不支持</option></select>
  return <div className="flex gap-1">
    <select aria-label={`${rule.name}超限封禁`} className={cls} value={custom ? 'custom' : rule.banSeconds} onChange={(e) => { setCustom(e.target.value === 'custom'); change(e.target.value === 'custom' ? 3600 : Number(e.target.value)) }}>
      <option value={0}>不封禁</option><option value={3600}>1 小时</option><option value={86400}>1 天</option><option value={604800}>7 天</option><option value={-1}>永久</option><option value="custom">自定义小时数</option>
    </select>
    {custom && <input aria-label={`${rule.name}自定义封禁小时数`} className={cls} type="number" min={1 / 3600} step="any" value={rule.banSeconds / 3600} onChange={(e) => change(Math.round(Number(e.target.value) * 3600))} />}
  </div>
}

export function RateLimitsPage() {
  const [settings, setSettings] = useState<AdminRateLimitSettings | null>(null)
  const [rules, setRules] = useState<AdminRateLimitRule[]>([])
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const setToast = useUiStore((s) => s.setToast)
  const dirty = settings !== null && JSON.stringify(rules) !== JSON.stringify(settings.rules)
  useEffect(() => {
    let cancelled = false
    adminRateLimitRules().then((value) => { if (!cancelled) { setSettings(value); setRules(value.rules) } }).catch((e: unknown) => { if (!cancelled) setError(e instanceof Error ? e.message : '加载失败') })
    return () => { cancelled = true }
  }, [])
  useEffect(() => {
    if (!dirty) return
    const warn = (e: BeforeUnloadEvent) => { e.preventDefault() }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty])
  function update(key: string, patch: Partial<AdminRateLimitRule>) { setRules((current) => current.map((r) => r.key === key ? { ...r, ...patch } : r)) }
  async function save() {
    setSaving(true); setError('')
    try {
      const result = await adminUpdateRateLimitRules(rules.map(({ key, limit, windowSeconds, banSeconds, enabled }) => ({ key, limit, windowSeconds, banSeconds, enabled })))
      setSettings(result); setRules(result.rules); setToast('限流规则已保存')
    } catch (e) { setError(e instanceof Error ? e.message : '保存失败') } finally { setSaving(false) }
  }
  return <form className="space-y-5" onSubmit={(e) => { e.preventDefault(); void save() }}>
    <h1 className="text-xl font-semibold text-lobby-ink">限流规则</h1>
    {settings && settings.multiplier > 1 && <p className="text-sm text-lobby-sub">当前运行时乘数 ×{settings.multiplier}（测试环境）</p>}
    {error && <p role="alert" className="text-sm text-red-600">{error}</p>}
    {!settings && !error && <p>加载中…</p>}
    {Object.entries(GROUPS).map(([group, title]) => {
      const items = rules.filter((r) => r.group === group)
      if (!items.length) return null
      return <section key={group} className="space-y-2"><h2 className="font-semibold text-lobby-ink">{title}</h2>
        <div className="overflow-x-auto rounded-card border border-lobby-line bg-lobby-card"><table className="w-full min-w-[780px] text-left text-sm">
          <thead><tr>{['名称', '次数', '时间窗口', '超限封禁', '启用'].map((n) => <th key={n} className="px-3 py-2">{n}</th>)}</tr></thead>
          <tbody>{items.map((r) => <tr key={r.key} className="border-t border-lobby-line">
            <td className="px-3 py-3">{r.name}</td>
            <td className="px-3 py-3">{r.kind === 'concurrent' && '最多 '}<input aria-label={`${r.name}次数`} className={cls} type="number" min={1} max={r.kind === 'concurrent' ? 100 : 100000} value={r.limit} onChange={(e) => update(r.key, { limit: Number(e.target.value) })} />{r.kind === 'concurrent' && ' 条连接'}</td>
            <td className="px-3 py-3">{r.kind === 'frequency' ? <WindowInput rule={r} change={(windowSeconds) => update(r.key, { windowSeconds })} /> : '—'}</td>
            <td className="px-3 py-3"><BanInput rule={r} change={(banSeconds) => update(r.key, { banSeconds })} /></td>
            <td className="px-3 py-3"><input aria-label={`${r.name}启用`} type="checkbox" checked={r.enabled} onChange={(e) => update(r.key, { enabled: e.target.checked })} /></td>
          </tr>)}</tbody>
        </table></div>
      </section>
    })}
    {settings && <div className="flex items-center gap-4"><button type="submit" disabled={!dirty || saving} className="rounded-full bg-brand px-5 py-2 text-white disabled:opacity-50">{saving ? '保存中…' : '保存'}</button>{dirty && <span className="text-sm text-lobby-sub">有未保存的改动</span>}</div>}
  </form>
}
