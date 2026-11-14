/** 正在输入的三个呼吸光点（how.md 1.2），依次延迟 0.2 秒。 */
export function TypingDots({ label = '对方正在输入…', dotsOnly = false }: { label?: string; dotsOnly?: boolean }) {
  return (
    <span className="inline-flex items-center gap-2" data-testid="typing-dots" aria-label={label}>
      <span className="inline-flex items-center gap-1">
        {[0, 1, 2].map((i) => (
          <span
            key={i}
            className="animate-breathe inline-block h-1.5 w-1.5 rounded-full bg-chat-acc"
            style={{ animationDelay: `${i * 0.2}s` }}
          />
        ))}
      </span>
      {!dotsOnly && <span className="text-xs text-night-sub">{label}</span>}
    </span>
  )
}
