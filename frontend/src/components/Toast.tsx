import { useEffect } from 'react'
import { useUiStore } from '../stores/ui'

/** 底部轻提示，3 秒后自动消失。 */
export function Toast() {
  const toast = useUiStore((s) => s.toast)
  const clear = useUiStore((s) => s.clearToast)

  useEffect(() => {
    if (!toast) return
    const t = setTimeout(clear, 3000)
    return () => clearTimeout(t)
  }, [toast, clear])

  if (!toast) return null
  return (
    <div
      role="status"
      className="animate-fade-up fixed bottom-6 left-1/2 z-50 -translate-x-1/2 rounded-full bg-lobby-ink/90 px-4 py-2 text-sm text-white shadow-soft"
    >
      {toast}
    </div>
  )
}
