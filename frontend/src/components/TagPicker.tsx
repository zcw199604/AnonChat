import { MAX_TAGS, TAGS } from '../lib/profileConsts'

interface TagPickerProps {
  selected: string[]
  onChange: (tags: string[]) => void
  disabled?: boolean
}

/** 标签选择：从预设列表中最多选 5 个，激活态为品牌紫。 */
export function TagPicker({ selected, onChange, disabled }: TagPickerProps) {
  function toggle(tag: string) {
    if (disabled) return
    if (selected.includes(tag)) {
      onChange(selected.filter((t) => t !== tag))
      return
    }
    if (selected.length >= MAX_TAGS) return
    onChange([...selected, tag])
  }

  return (
    <div className="flex flex-wrap gap-1.5" aria-label="标签">
      {TAGS.map((tag) => {
        const active = selected.includes(tag)
        return (
          <button
            key={tag}
            type="button"
            disabled={disabled}
            aria-pressed={active}
            onClick={() => toggle(tag)}
            className={`rounded-sm px-2.5 py-1 text-xs transition duration-150 ease-out disabled:opacity-40 ${
              active
                ? 'bg-brand/15 text-brand'
                : 'bg-lobby-bg text-lobby-sub hover:text-lobby-ink'
            }`}
          >
            {tag}
          </button>
        )
      })}
      <span className="self-center pl-1 text-xs text-lobby-sub">
        {selected.length}/{MAX_TAGS}
      </span>
    </div>
  )
}
