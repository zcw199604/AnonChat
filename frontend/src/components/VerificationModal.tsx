import { useEffect, useState } from 'react'
import { sendSMSCode, confirmSMSCode } from '../lib/api/client'
import { errorMessage } from '../lib/api/errorMessages'
import { useIdentityStore } from '../stores/identity'
import { useUiStore } from '../stores/ui'

/** 实名验证弹窗：输入手机号 → 发送验证码（60 秒倒计时）→ 输入验证码确认。 */
export function VerificationModal() {
  const open = useUiStore((s) => s.verificationOpen)
  const close = useUiStore((s) => s.closeVerification)
  const setToast = useUiStore((s) => s.setToast)
  const setVerified = useIdentityStore((s) => s.setVerified)
  const mode = useIdentityStore((s) => s.verificationMode)

  const [phone, setPhone] = useState('')
  const [code, setCode] = useState('')
  const [countdown, setCountdown] = useState(0)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (countdown <= 0) return
    const t = setTimeout(() => setCountdown((c) => c - 1), 1000)
    return () => clearTimeout(t)
  }, [countdown])

  if (!open || mode === 'off') return null

  async function send() {
    try {
      setBusy(true)
      await sendSMSCode(phone)
      setCountdown(60)
      setToast('验证码已发送')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    } finally {
      setBusy(false)
    }
  }

  async function confirm() {
    try {
      setBusy(true)
      await confirmSMSCode(phone, code)
      setVerified(true)
      setToast('验证成功')
      close()
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 bg-black/60 flex items-center justify-center p-4 z-50" role="dialog" aria-label="实名验证">
      <div className="bg-slate-900 rounded-lg p-4 w-full max-w-sm space-y-3">
        <h2 className="text-lg font-semibold">实名验证</h2>
        <p className="text-sm text-slate-400">完成手机号验证后即可开始聊天。其他用户看不到你的手机号。</p>
        <input
          aria-label="手机号"
          className="w-full bg-slate-800 rounded px-3 py-2"
          placeholder="手机号"
          value={phone}
          onChange={(e) => setPhone(e.target.value)}
        />
        <div className="flex gap-2">
          <button
            type="button"
            className="px-3 py-2 rounded bg-slate-700 disabled:opacity-40"
            disabled={busy || countdown > 0 || phone === ''}
            onClick={send}
          >
            {countdown > 0 ? `${countdown} 秒后可重发` : '发送验证码'}
          </button>
        </div>
        <input
          aria-label="验证码"
          className="w-full bg-slate-800 rounded px-3 py-2"
          placeholder="验证码"
          value={code}
          onChange={(e) => setCode(e.target.value)}
        />
        <div className="flex justify-end gap-2">
          <button type="button" className="px-3 py-1 rounded bg-slate-700" onClick={close}>
            稍后再说
          </button>
          <button
            type="button"
            className="px-3 py-1 rounded bg-indigo-600 disabled:opacity-40"
            disabled={busy || code === ''}
            onClick={confirm}
          >
            确认
          </button>
        </div>
      </div>
    </div>
  )
}
