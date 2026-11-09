import { useState } from 'react'
import { adminLogin } from '../../lib/api/client'
import { Button, Field, Panel, inputClass } from './ui'

/** 管理员登录页：表单提交（回车可登录），用户名/密码各有可见标签。 */
export function LoginPage({
  onSuccess,
  onError,
}: {
  onSuccess: () => void
  onError: (e: unknown) => void
}) {
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)

  const canSubmit = username.trim() !== '' && password !== '' && !busy

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    if (!canSubmit) return
    setBusy(true)
    try {
      await adminLogin(username.trim(), password)
      onSuccess()
    } catch (err) {
      onError(err)
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="flex min-h-screen items-center justify-center px-4 py-10">
      <Panel className="w-full max-w-sm p-6">
        <h1 className="text-xl font-semibold text-lobby-ink">管理员登录</h1>
        <p className="mt-1 mb-5 text-sm text-lobby-sub">仅限内部人员使用，登录后可审核内容与查看审计日志。</p>
        <form className="space-y-3" onSubmit={(e) => void submit(e)}>
          <Field label="用户名">
            <input
              aria-label="用户名"
              autoComplete="username"
              className={inputClass}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
            />
          </Field>
          <Field label="密码">
            <input
              aria-label="密码"
              type="password"
              autoComplete="current-password"
              className={inputClass}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
          </Field>
          <Button type="submit" variant="primary" className="w-full" loading={busy} disabled={!canSubmit}>
            登录
          </Button>
        </form>
      </Panel>
    </main>
  )
}
