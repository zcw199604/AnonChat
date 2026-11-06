import { useState } from 'react'
import { Avatar } from '../components/Avatar'
import { useIdentityStore } from '../stores/identity'
import { errorMessage } from '../lib/api/errorMessages'
import { useUiStore } from '../stores/ui'

/** 顶部栏：头像、昵称、换个身份（二次确认）。 */
export function TopBar() {
  const nickname = useIdentityStore((s) => s.nickname)
  const avatarSeed = useIdentityStore((s) => s.avatarSeed)
  const reset = useIdentityStore((s) => s.reset)
  const setToast = useUiStore((s) => s.setToast)
  const connected = useUiStore((s) => s.connected)
  const [confirming, setConfirming] = useState(false)

  async function doReset() {
    setConfirming(false)
    try {
      await reset()
      setToast('已更换身份')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  return (
    <header className="border-b border-slate-800 px-4 py-2 flex items-center justify-between">
      <div className="flex items-center gap-2">
        <Avatar seed={avatarSeed || 'anonymous'} size={32} />
        <span className="text-sm text-slate-200">{nickname || '加载中…'}</span>
        {connected && <span data-testid="ws-connected" aria-label="已连接" className="w-2 h-2 rounded-full bg-green-500" />}
      </div>
      <button
        type="button"
        className="text-sm px-3 py-1 rounded bg-slate-800 hover:bg-slate-700"
        onClick={() => setConfirming(true)}
      >
        换个身份
      </button>
      {confirming && (
        <div className="fixed inset-0 bg-black/60 flex items-center justify-center p-4 z-50" role="dialog" aria-label="换身份确认">
          <div className="bg-slate-900 rounded-lg p-4 w-full max-w-sm">
            <p className="mb-4 text-slate-200">确定要换个身份吗？当前的聊天会全部离开。</p>
            <div className="flex justify-end gap-2">
              <button type="button" className="px-3 py-1 rounded bg-slate-700" onClick={() => setConfirming(false)}>
                取消
              </button>
              <button type="button" className="px-3 py-1 rounded bg-indigo-600" onClick={doReset}>
                确认更换
              </button>
            </div>
          </div>
        </div>
      )}
    </header>
  )
}
