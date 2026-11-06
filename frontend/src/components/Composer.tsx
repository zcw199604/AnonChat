import { useRef, useState } from 'react'

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
  maxChars?: number
  disabled?: boolean
}

const TYPING_THROTTLE_MS = 3000

/** 输入框：Enter 发送/Shift+Enter 换行、字数限制、表情面板、输入中节流。暗色玻璃风格。 */
export function Composer({ onSend, onTyping, maxChars = 1000, disabled }: ComposerProps) {
  const [value, setValue] = useState('')
  const [showEmoji, setShowEmoji] = useState(false)
  const lastTyping = useRef(Number.NEGATIVE_INFINITY)

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
    <div className="border-t border-glass-line p-3">
      {showEmoji && (
        <div
          className="mb-2 grid max-h-40 grid-cols-10 gap-1 overflow-y-auto"
          data-testid="emoji-panel"
        >
          {EMOJIS.map((e) => (
            <button
              type="button"
              key={e}
              className="rounded-sm text-lg transition duration-150 ease-out hover:bg-glass"
              onClick={() => handleChange(value + e)}
            >
              {e}
            </button>
          ))}
        </div>
      )}
      <div className="flex items-end gap-2">
        <button
          type="button"
          aria-label="表情"
          className="rounded-sm px-2 py-1 text-night-ink transition duration-150 ease-out hover:bg-glass"
          onClick={() => setShowEmoji((v) => !v)}
        >
          😊
        </button>
        <textarea
          aria-label="消息输入框"
          className="flex-1 resize-none rounded-sm border border-glass-line bg-glass px-3 py-2 text-night-ink outline-none placeholder:text-night-sub"
          rows={2}
          value={value}
          disabled={disabled}
          onChange={(e) => handleChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && !e.shiftKey) {
              e.preventDefault()
              submit()
            }
          }}
        />
        <button
          type="button"
          className="h-11 w-11 shrink-0 rounded-full text-sm font-medium text-white transition duration-150 ease-out disabled:opacity-40"
          style={{ background: 'linear-gradient(135deg, var(--color-brand), var(--color-accent))' }}
          disabled={!canSend}
          onClick={submit}
        >
          发送
        </button>
      </div>
      <div className={`mt-1 text-[11px] ${tooLong ? 'text-danger' : 'text-night-sub'}`}>
        {len} / {maxChars}
      </div>
    </div>
  )
}
