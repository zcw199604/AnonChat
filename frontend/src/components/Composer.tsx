import { useLayoutEffect, useRef, useState } from 'react'
import { Icon } from './Icon'

/** 约 100 个常用表情（直接内置，不引入库）。 */
const EMOJIS = [
  '😀', '😃', '😄', '😁', '😆', '😅', '🤣', '😂', '🙂', '🙃',
  '😉', '😊', '😇', '🥰', '😍', '🤩', '😘', '😗', '😚', '😙',
  '😋', '😛', '😜', '🤪', '😝', '🤑', '🤗', '🤭', '🤫', '🤔',
  '🤐', '🤨', '😐', '😑', '😶', '😏', '😒', '🙄', '😬', '🤥',
  '😌', '😔', '😪', '🤤', '😴', '😷', '🤒', '🤕', '🤢', '🤮',
  '🤧', '🥵', '🥶', '🥴', '😵', '🤯', '🤠', '🥳', '😎', '🤓',
  '🧐', '😕', '😟', '🙁', '😮', '😯', '😲', '😳', '🥺', '😦',
  '😧', '😨', '😰', '😥', '😢', '😭', '😱', '😖', '😣', '😞',
  '😓', '😩', '😫', '🥱', '😤', '😡', '😠', '🤬', '😈', '💀',
  '👻', '👽', '🤖', '💩', '🤡', '❤️', '👍', '👏', '🙏', '🎉',
]

interface ComposerProps {
  onSend: (body: string) => void
  onTyping?: () => void
  onSendMedia?: (file: File) => Promise<void>
  maxChars?: number
  disabled?: boolean
}

const TYPING_THROTTLE_MS = 3000

/** 输入框：Enter 发送/Shift+Enter 换行、字数限制、表情面板、输入中节流。跟随白天/夜晚主题。 */
export function Composer({ onSend, onTyping, onSendMedia, maxChars = 1000, disabled }: ComposerProps) {
  const [value, setValue] = useState('')
  const [showEmoji, setShowEmoji] = useState(false)
  const lastTyping = useRef(Number.NEGATIVE_INFINITY)
  const fileInput = useRef<HTMLInputElement>(null)
  const textarea = useRef<HTMLTextAreaElement>(null)
  const [uploading, setUploading] = useState(false)
  const [mediaError, setMediaError] = useState('')

  useLayoutEffect(() => {
    const el = textarea.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = `${Math.max(24, Math.min(el.scrollHeight, 120))}px`
    el.style.overflowY = el.scrollHeight > 120 ? 'auto' : 'hidden'
  }, [value])

  async function sendFile(file: File) {
    if (!onSendMedia || uploading || disabled) return
    setUploading(true)
    setMediaError('')
    try {
      await onSendMedia(file)
    } catch (e) {
      setMediaError(e instanceof Error ? e.message : '媒体发送失败，请重试')
    } finally {
      setUploading(false)
    }
  }

  const len = [...value].length
  const tooLong = len > maxChars
  const canSend = value.trim().length > 0 && !tooLong && !disabled

  function submit() {
    if (!canSend) return
    onSend(value.trim())
    setValue('')
    setShowEmoji(false)
  }

  function handleChange(next: string) {
    setValue(next)
    const now = Date.now()
    if (now - lastTyping.current >= TYPING_THROTTLE_MS) {
      lastTyping.current = now
      onTyping?.()
    }
  }

  return (
    <div className="border-t border-glass-line bg-[rgb(var(--c-ov-rgb)/0.02)] p-3 pb-[max(12px,env(safe-area-inset-bottom))]">
      <div className="flex items-end gap-2">
        {onSendMedia && (
          <>
            <input
              ref={fileInput}
              className="hidden"
              type="file"
              aria-label="选择图片或视频"
              accept="image/jpeg,image/png,image/gif,image/webp,video/mp4,video/webm"
              disabled={disabled || uploading}
              onChange={(e) => {
                const file = e.target.files?.[0]
                e.target.value = ''
                if (file) void sendFile(file)
              }}
            />
            <button type="button" aria-label="发送图片或视频"
              className="flex h-11 w-11 shrink-0 items-center justify-center rounded-full bg-glass text-night-ink disabled:opacity-40"
              disabled={disabled || uploading} onClick={() => fileInput.current?.click()}>
              {uploading
                ? <span aria-hidden="true" className="h-[18px] w-[18px] animate-spin rounded-full border-2 border-current border-t-transparent" />
                : <Icon name="plus" />}
            </button>
          </>
        )}
        <div className="flex min-h-11 min-w-0 flex-1 items-end rounded-[22px] border border-glass-line bg-glass focus-within:border-[rgb(var(--c-acc-rgb)/0.55)]">
          <textarea ref={textarea} aria-label="消息输入框" placeholder="说点什么…"
            className="my-[9px] min-w-0 flex-1 resize-none bg-transparent px-3.5 text-[15px] leading-6 text-night-ink outline-none placeholder:text-night-sub"
            rows={1} value={value} disabled={disabled}
            onChange={(e) => handleChange(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                submit()
              }
            }}
          />
          <button type="button" aria-label="表情" aria-expanded={showEmoji}
            className={`mb-px flex h-10 w-10 shrink-0 items-center justify-center rounded-full ${showEmoji ? 'text-chat-acc' : 'text-night-sub'}`}
            onClick={() => setShowEmoji((v) => !v)}>
            <Icon name="smile" size={22} />
          </button>
        </div>
        <button type="button" aria-label="发送"
          className={`flex h-11 w-11 shrink-0 items-center justify-center rounded-full transition duration-150 ease-out ${canSend ? 'scale-100 bg-bubble-self text-white' : 'scale-[0.92] bg-glass text-night-sub'}`}
          disabled={!canSend} onClick={submit}>
          <Icon name="send" />
        </button>
      </div>
      {len > maxChars * 0.9 && (
        <div className={`mt-1 text-[11px] ${tooLong ? 'text-chat-danger' : 'text-night-sub'}`}>
          {len} / {maxChars}
        </div>
      )}
      {showEmoji && (
        <div className="mt-2 grid max-h-[248px] grid-cols-8 gap-1 overflow-y-auto sm:grid-cols-10" data-testid="emoji-panel">
          {EMOJIS.map((e) => (
            <button type="button" key={e}
              className="h-11 rounded-sm text-2xl transition duration-150 ease-out hover:bg-glass"
              onClick={() => handleChange(value + e)}>{e}</button>
          ))}
        </div>
      )}
      {mediaError && <p role="alert" className="mt-1 text-xs text-chat-danger">{mediaError}</p>}
    </div>
  )
}
