interface RangeSliderProps {
  value: number | null
  onChange: (v: number | null) => void
  min: number
  max: number
  ariaLabel: string
  disabled?: boolean
}

/** 滑杆（12px 圆角、品牌紫激活态），带「不填」清空入口。 */
export function RangeSlider({ value, onChange, min, max, ariaLabel, disabled }: RangeSliderProps) {
  const current = value ?? min
  return (
    <div className="flex items-center gap-3">
      <input
        type="range"
        aria-label={ariaLabel}
        min={min}
        max={max}
        step={1}
        disabled={disabled}
        value={current}
        onChange={(e) => onChange(Number(e.target.value))}
        className="h-1.5 flex-1 cursor-pointer appearance-none rounded-sm bg-lobby-line accent-brand disabled:opacity-40"
      />
      <span className="w-12 text-right text-sm tabular-nums text-lobby-ink">
        {value === null ? '保密' : value}
      </span>
      <button
        type="button"
        disabled={disabled || value === null}
        onClick={() => onChange(null)}
        className="rounded-sm px-2 py-0.5 text-xs text-lobby-sub transition duration-150 ease-out hover:bg-lobby-bg disabled:opacity-30"
      >
        不填
      </button>
    </div>
  )
}
