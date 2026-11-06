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

/** 输入框：Enter 发送/Shift+Enter 换行、字数限制、表情面板、输入中节流。 */
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
    <div className="border-t border-slate-800 p-2">
      {showEmoji && (
        <div className="grid grid-cols-10 gap-1 mb-2 max-h-40 overflow-y-auto" data-testid="emoji-panel">
          {EMOJIS.map((e) => (
            <button
              type="button"
              key={e}
              className="text-lg hover:bg-slate-800 rounded"
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
          className="px-2 py-1 rounded hover:bg-slate-800"
          onClick={() => setShowEmoji((v) => !v)}
        >
          😊
        </button>
        <textarea
          aria-label="消息输入框"
          className="flex-1 resize-none bg-slate-800 rounded px-3 py-2 text-slate-100 outline-none"
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
          className="px-3 py-2 rounded bg-indigo-600 disabled:opacity-40"
          disabled={!canSend}
          onClick={submit}
        >
          发送
        </button>
      </div>
      <div className={`text-xs mt-1 ${tooLong ? 'text-red-400' : 'text-slate-500'}`}>
        {len} / {maxChars}
      </div>
    </div>
  )
}
