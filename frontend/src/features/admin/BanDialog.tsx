import { useState } from 'react'
import { adminCreateBan } from '../../lib/api/client'
import { useUiStore } from '../../stores/ui'
import { BAN_DURATIONS, isValidIP } from './banForm'
import { useAdmin } from './context'
import { Button, FilterTabs, Modal, inputClass } from './ui'

/** 封禁弹窗的目标：按用户封（可勾选匿名身份/实名账号/各个 IP）或按 IP 封。 */
export type BanTarget =
  | {
      kind: 'user'
      identityId: string
      nickname: string
      publicCode: string
      /** 未实名时为空字符串。 */
      accountId: string
      ips: string[]
    }
  | { kind: 'ip'; ip?: string }

interface Subject {
  type: 'identity' | 'account' | 'ip'
  value: string
}

/**
 * 通用封禁弹窗：勾选主体 + 时长 + 原因，提交后回调 onDone。
 * 表单初值由 target 推导，换 target 时调用方需用 key 触发重新挂载（见 BansPage）。
 * 仅供已登录的后台页面使用；错误统一交给 useAdmin().onError。
 */
export function BanDialog({
  open,
  onClose,
  onDone,
  target,
}: {
  open: boolean
  onClose: () => void
  onDone: () => void
  target: BanTarget | null
}) {
  const { onError } = useAdmin()
  const setToast = useUiStore((s) => s.setToast)

  // 勾选状态以 target 为初值：匿名身份默认勾选，实名账号与各个 IP 默认不勾。
  const [identity, setIdentity] = useState(target?.kind === 'user')
  const [account, setAccount] = useState(false)
  const [selectedIPs, setSelectedIPs] = useState<string[]>([])
  const [ipInput, setIpInput] = useState(target?.kind === 'ip' ? (target.ip ?? '') : '')
  const [durationHours, setDurationHours] = useState(24)
  const [reason, setReason] = useState('')
  const [errors, setErrors] = useState<string[]>([])
  const [busy, setBusy] = useState(false)

  function toggleIP(ip: string, checked: boolean) {
    setSelectedIPs((prev) => (checked ? [...prev, ip] : prev.filter((v) => v !== ip)))
  }

  /** 依据当前目标与勾选状态收集封禁主体。 */
  function collectSubjects(): Subject[] {
    if (target?.kind === 'user') {
      const out: Subject[] = []
      if (identity) out.push({ type: 'identity', value: target.identityId })
      if (account && target.accountId) out.push({ type: 'account', value: target.accountId })
      for (const ip of selectedIPs) out.push({ type: 'ip', value: ip })
      return out
    }
    const ip = ipInput.trim()
    return ip ? [{ type: 'ip', value: ip }] : []
  }

  function validate(): string[] {
    const problems: string[] = []
    if (collectSubjects().length === 0) {
      problems.push('请至少选择一个封禁对象')
    }
    if (target?.kind === 'ip' && ipInput.trim() !== '' && !isValidIP(ipInput)) {
      problems.push('IP 格式不正确')
    }
    if (reason.trim() === '') {
      problems.push('请填写封禁原因')
    }
    return problems
  }

  async function submit() {
    const problems = validate()
    setErrors(problems)
    if (problems.length > 0) return
    setBusy(true)
    try {
      await adminCreateBan({
        subjects: collectSubjects(),
        durationHours: durationHours === 0 ? null : durationHours,
        reason: reason.trim(),
      })
      setToast('已封禁')
      onDone()
    } catch (e) {
      onError(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal
      open={open}
      title={target?.kind === 'ip' ? '封禁 IP' : '封禁用户'}
      description={
        target?.kind === 'user' ? (
          <span>
            <span className="text-lobby-ink">{target.nickname || '未命名'}</span>
            <span className="ml-2 font-mono text-xs">{target.publicCode}</span>
          </span>
        ) : (
          '该 IP 上的全部连接会在封禁生效后被断开。'
        )
      }
      size="md"
      closeDisabled={busy}
      onClose={onClose}
      footer={
        <>
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            取消
          </Button>
          <Button variant="danger" loading={busy} onClick={() => void submit()}>
            确认封禁
          </Button>
        </>
      }
    >
      <div className="space-y-3 pb-1">
        {target?.kind === 'user' ? (
          <div className="space-y-1">
            <span className="block text-xs font-medium text-lobby-sub">封禁对象</span>
            <div className="flex flex-wrap gap-3 text-sm text-lobby-ink">
              <label className="flex items-center gap-1.5">
                <input
                  type="checkbox"
                  checked={identity}
                  onChange={(e) => setIdentity(e.target.checked)}
                />
                匿名身份
              </label>
              {target.accountId && (
                <label className="flex items-center gap-1.5">
                  <input
                    type="checkbox"
                    checked={account}
                    onChange={(e) => setAccount(e.target.checked)}
                  />
                  实名账号
                </label>
              )}
              {target.ips.map((ip) => (
                <label key={ip} className="flex items-center gap-1.5">
                  <input
                    type="checkbox"
                    checked={selectedIPs.includes(ip)}
                    onChange={(e) => toggleIP(ip, e.target.checked)}
                  />
                  <span className="font-mono text-xs">{ip}</span>
                </label>
              ))}
            </div>
          </div>
        ) : (
          <label className="block space-y-1">
            <span className="block text-xs font-medium text-lobby-sub">IP 地址</span>
            <input
              aria-label="IP 地址"
              className={`${inputClass} font-mono`}
              placeholder="例如 203.0.113.7 或 2001:db8::1"
              value={ipInput}
              onChange={(e) => setIpInput(e.target.value)}
            />
          </label>
        )}

        <div className="space-y-1">
          <span className="block text-xs font-medium text-lobby-sub">封禁时长</span>
          <FilterTabs<string>
            ariaLabel="封禁时长"
            value={String(durationHours)}
            onChange={(v) => setDurationHours(Number(v))}
            options={BAN_DURATIONS.map((d) => ({ value: String(d.value), label: d.label }))}
          />
        </div>

        <label className="block space-y-1">
          <span className="block text-xs font-medium text-lobby-sub">封禁原因</span>
          <textarea
            aria-label="封禁原因"
            rows={3}
            className={inputClass}
            placeholder="必填，会写入审计日志"
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </label>

        {errors.length > 0 && <div className="text-xs text-danger">{errors.join('；')}</div>}
      </div>
    </Modal>
  )
}
