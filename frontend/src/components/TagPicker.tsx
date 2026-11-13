import { useState } from 'react'
import { MAX_TAGS, TAGS } from '../lib/profileConsts'

interface TagPickerProps {
  selected: string[]
  onChange: (tags: string[]) => void
  disabled?: boolean
  /**
   * 传入后启用折叠模式（默认折叠）：只显示 collapsedCount 个标签（已选优先，按 TAGS 顺序），
   * 其余收进「更多 +N」按钮；不传时保持原样，渲染全部标签。
   */
  collapsedCount?: number
}

/**
 * 标签选择：从预设列表中最多选 5 个，激活态为品牌紫。
 * 可选折叠：资料卡里标签太多会把匹配按钮挤出移动端首屏，因此首页传 collapsedCount 收窄默认展示。
 */
export function TagPicker({ selected, onChange, disabled, collapsedCount }: TagPickerProps) {
  const [expanded, setExpanded] = useState(false)

  function toggle(tag: string) {
    if (disabled) return
    if (selected.includes(tag)) {
      onChange(selected.filter((t) => t !== tag))
      return
    }
    if (selected.length >= MAX_TAGS) return
    onChange([...selected, tag])
  }

  const folded = collapsedCount !== undefined && !expanded

  // 折叠模式：已选标签（按 TAGS 顺序）先显示，再用未选标签补足到 collapsedCount 个。
  const orderedSelected = [
    ...TAGS.filter((t) => selected.includes(t)),
    ...selected.filter((t) => !(TAGS as readonly string[]).includes(t)),
  ]
  const visibleTags = folded
    ? orderedSelected.length >= (collapsedCount as number)
      ? orderedSelected
      : [...orderedSelected, ...TAGS.filter((t) => !selected.includes(t))].slice(0, collapsedCount as number)
    : [...TAGS]
  const hiddenCount = folded ? TAGS.filter((t) => !visibleTags.includes(t)).length : 0

  return (
    <div className="flex flex-wrap gap-1.5" aria-label="标签">
      {visibleTags.map((tag) => {
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
      {hiddenCount > 0 && (
        <button
          type="button"
          disabled={disabled}
          aria-expanded={false}
          onClick={() => setExpanded(true)}
          className="rounded-sm border border-dashed border-lobby-line px-2.5 py-1 text-xs text-lobby-sub transition duration-150 ease-out hover:text-lobby-ink disabled:opacity-40"
        >
          更多 +{hiddenCount}
        </button>
      )}
      {folded === false && collapsedCount !== undefined && (
        <button
          type="button"
          disabled={disabled}
          aria-expanded
          onClick={() => setExpanded(false)}
          className="rounded-sm border border-dashed border-lobby-line px-2.5 py-1 text-xs text-lobby-sub transition duration-150 ease-out hover:text-lobby-ink disabled:opacity-40"
        >
          收起
        </button>
      )}
      <span className="self-center pl-1 text-xs text-lobby-sub">
        {selected.length}/{MAX_TAGS}
      </span>
    </div>
  )
}
