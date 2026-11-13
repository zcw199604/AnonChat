import { useEffect, useState } from 'react'
import { adminExternalSettings, adminExternalStatus, adminSaveExternalSettings } from '../externalApi'
import type { ExternalStatus } from '../externalApi'
import { EXTERNAL_FIELDS, externalPayload, externalToForm, validateExternalForm } from '../externalForm'
import type { ExternalForm } from '../externalForm'
import { useAdmin } from '../context'
import { Button, LoadingRows, PageHeader, Panel, inputClass } from '../ui'
import { formatTime } from '../labels'
import { useUiStore } from '../../../stores/ui'

const COUNTER_NAMES: Record<string, string> = {
  started: '开始', found: '匹配成功', cancelled: '取消', relayedIn: '入站',
  relayedOut: '出站', mediaFailed: '媒体失败', textFailed: '文本失败', pendingDropped: '丢弃',
}
const SWITCHES = [
  { key: 'enabled', label: '总开关' },
  { key: 'chat2019Enabled', label: '启用 chat2019' },
  { key: 'wanfengEnabled', label: '启用晚风' },
] as const

function timestamp(value: string) {
  return !value || value.startsWith('0001-') ? '—' : formatTime(value)
}

export function ExternalPage() {
  const { onError } = useAdmin()
  const setToast = useUiStore((s) => s.setToast)
  const [form, setForm] = useState<ExternalForm | null>(null)
  const [status, setStatus] = useState<ExternalStatus | null>(null)
  const [errors, setErrors] = useState<string[]>([])
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let cancelled = false
    void adminExternalSettings()
      .then((s) => { if (!cancelled) setForm(externalToForm(s)) })
      .catch((e) => { if (!cancelled) onError(e) })
    // 定时刷新只更新状态，保留管理员尚未保存的表单。
    const refresh = () => {
      void adminExternalStatus()
        .then((s) => { if (!cancelled) setStatus(s) })
        .catch((e) => { if (!cancelled) onError(e) })
    }
    refresh()
    const timer = setInterval(refresh, 10000)
    return () => { cancelled = true; clearInterval(timer) }
  }, [onError])

  async function save() {
    if (!form) return
    const next = validateExternalForm(form)
    setErrors(next)
    if (next.length) return
    setBusy(true)
    try {
      const saved = await adminSaveExternalSettings(externalPayload(form))
      setForm(externalToForm(saved))
      setToast('外站设置已保存')
      setStatus(await adminExternalStatus())
    } catch (e) {
      onError(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div>
      <PageHeader title="外站接入" description="配置外站匹配时机与连接上限，状态每 10 秒刷新。" />
      <Panel className="mb-4 space-y-3 p-4">
        <p className="text-sm text-danger">关闭总开关会立即结束所有进行中的外站会话</p>
        <p className="text-sm text-lobby-sub">
          开启外站接入后，Bot 匹配兜底时间不再生效；兜底在「本站等待 + 外站匹配时长」结束后立即触发。关闭后恢复原逻辑。
        </p>
        {!form ? <LoadingRows /> : (
          <form noValidate onSubmit={(e) => { e.preventDefault(); void save() }} className="space-y-4">
            <fieldset disabled={busy} className="space-y-4">
              <div className="flex flex-wrap gap-5 text-sm">
                {SWITCHES.map(({ key, label }) => (
                  <label key={key} className="flex items-center gap-2">
                    <input type="checkbox" checked={form[key]} onChange={(e) => setForm({ ...form, [key]: e.target.checked })} />
                    {label}
                  </label>
                ))}
              </div>
              <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
                {EXTERNAL_FIELDS.map(({ key, label, min, max }) => (
                  <label key={key} className="space-y-1 text-sm text-lobby-sub">
                    <span>{label}</span>
                    <input aria-label={label} type="number" min={min} max={max} step="1" className={inputClass}
                      value={form[key]} onChange={(e) => setForm({ ...form, [key]: e.target.value })} />
                  </label>
                ))}
              </div>
            </fieldset>
            {errors.length > 0 && <p role="alert" className="text-sm text-danger">{errors.join('；')}</p>}
            <Button type="submit" variant="primary" loading={busy}>保存设置</Button>
          </form>
        )}
      </Panel>
      <Panel className="overflow-x-auto p-4">
        <h2 className="mb-3 font-semibold">运行状态</h2>
        {status ? (
          <>
            <p className="mb-3 text-sm text-lobby-sub">在线 {status.online} · 排队 {status.queue}</p>
            <table className="w-full text-left text-sm">
              <thead><tr>
                {['站点', '启用 / 可用', '连接 / 匹配 / 会话 / 空闲', '冷却至', '最近错误', '计数'].map((label) => <th key={label} className="p-2">{label}</th>)}
              </tr></thead>
              <tbody>
                {status.providers.map((p) => (
                  <tr key={p.name} className="border-t border-lobby-line">
                    <td className="p-2">{p.name === 'wanfeng' ? '晚风' : p.name}</td>
                    <td className="p-2">
                      {p.enabled ? '开启' : '关闭'} / {p.available ? '可用' : '不可用'}
                      {p.reason && <div className="text-xs text-lobby-sub">{p.reason}</div>}
                    </td>
                    <td className="p-2">{p.links} / {p.matching} / {p.inRoom} / {p.idle}</td>
                    <td className="p-2">{timestamp(p.cooldownUntil)}</td>
                    <td className="p-2">
                      {p.lastError || '—'}
                      {p.lastError && <div className="text-xs">{timestamp(p.lastErrorAt)}</div>}
                    </td>
                    <td className="p-2">
                      {Object.entries(p.counters).map(([key, value]) => <span key={key} className="mr-3 inline-block">{COUNTER_NAMES[key] ?? key} {value}</span>)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        ) : <LoadingRows />}
      </Panel>
    </div>
  )
}
