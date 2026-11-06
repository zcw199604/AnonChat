interface SegmentedOption<T extends string> {
  value: T
  label: string
}

interface SegmentedProps<T extends string> {
  options: Array<SegmentedOption<T>>
  value: T
  onChange: (v: T) => void
  ariaLabel: string
  disabled?: boolean
}

/** 分段控件：胶囊容器 + 品牌紫激活态。 */
export function Segmented<T extends string>({
  options,
  value,
  onChange,
  ariaLabel,
  disabled,
}: SegmentedProps<T>) {
  return (
    <div
      role="group"
      aria-label={ariaLabel}
      className="inline-flex items-center gap-1 rounded-sm bg-lobby-bg p-1"
    >
      {options.map((o) => {
        const active = o.value === value
        return (
          <button
            key={o.value}
            type="button"
            disabled={disabled}
            aria-pressed={active}
            onClick={() => onChange(o.value)}
            className={`rounded-sm px-3 py-1 text-sm transition duration-150 ease-out disabled:opacity-40 ${
              active ? 'bg-brand text-white' : 'text-lobby-sub hover:text-lobby-ink'
            }`}
          >
            {o.label}
          </button>
        )
      })}
    </div>
  )
}
