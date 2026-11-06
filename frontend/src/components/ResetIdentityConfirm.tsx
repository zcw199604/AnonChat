import { useState } from 'react'
import { useIdentityStore } from '../stores/identity'
import { useUiStore } from '../stores/ui'
import { errorMessage } from '../lib/api/errorMessages'

interface ResetIdentityConfirmProps {
  /** 触发按钮的额外样式类。 */
  className?: string
  label?: string
  ariaLabel?: string
  onDone?: () => void
}

/**
 * 换个身份（二次确认，沿用 V1 逻辑）。
 * 抽屉导航与首页资料卡共用同一套确认与错误提示。
 */
export function ResetIdentityConfirm({
  className,
  label = '🔄 换个身份',
  ariaLabel = '换个身份',
  onDone,
}: ResetIdentityConfirmProps) {
  const reset = useIdentityStore((s) => s.reset)
  const setToast = useUiStore((s) => s.setToast)
  const [confirming, setConfirming] = useState(false)

  async function doReset() {
    setConfirming(false)
    try {
      await reset()
      setToast('已更换身份')
      onDone?.()
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  return (
    <>
      <button
        type="button"
        aria-label={ariaLabel}
        onClick={() => setConfirming(true)}
        className={
          className ??
          'rounded-sm px-2 py-1 text-xs text-lobby-sub transition duration-150 ease-out hover:bg-lobby-bg'
        }
      >
        {label}
      </button>
      {confirming && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
          role="dialog"
          aria-label="换身份确认"
        >
          <div className="w-full max-w-sm rounded-panel bg-lobby-card p-5 shadow-soft">
            <p className="mb-4 text-sm text-lobby-ink">
              确定要换个身份吗？当前的聊天会全部离开，资料重置，VIP 与剩余广播卡会转移到新身份。
            </p>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                className="rounded-sm px-3 py-1.5 text-sm text-lobby-sub transition duration-150 ease-out hover:bg-lobby-bg"
                onClick={() => setConfirming(false)}
              >
                取消
              </button>
              <button
                type="button"
                className="rounded-sm bg-brand px-3 py-1.5 text-sm text-white transition duration-150 ease-out"
                onClick={doReset}
              >
                确认更换
              </button>
            </div>
          </div>
        </div>
      )}
    </>
  )
}
