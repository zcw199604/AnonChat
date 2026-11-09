/** 管理后台封禁表单校验（纯函数）。 */

export interface BanFormState {
  identity: boolean
  ip: boolean
  account: boolean
  durationHours: number | null
  reason: string
}

export interface BanFormResult {
  ok: boolean
  errors: string[]
}

/** 封禁时长选项：0 表示永久（提交时转成 durationHours: null）。 */
export const BAN_DURATIONS = [
  { value: 24, label: '1 天' },
  { value: 168, label: '7 天' },
  { value: 720, label: '30 天' },
  { value: 0, label: '永久' },
]

export function validateBanForm(f: BanFormState): BanFormResult {
  const errors: string[] = []
  if (!f.identity && !f.ip && !f.account) {
    errors.push('请至少选择一个封禁对象')
  }
  if (f.reason.trim() === '') {
    errors.push('请填写封禁原因')
  }
  return { ok: errors.length === 0, errors }
}

/** 把勾选项转换为 subjects 类型列表。 */
export function banSubjects(f: BanFormState): string[] {
  const out: string[] = []
  if (f.identity) out.push('identity')
  if (f.ip) out.push('ip')
  if (f.account) out.push('account')
  return out
}

const IPV4_RE = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/
const IPV6_CHARS_RE = /^[0-9a-fA-F:]+$/

/**
 * IP 字面量的宽松校验：IPv4 校验每段 0-255（不允许前导零），
 * IPv6 只校验字符集、冒号数量与是否出现连续三个冒号。后端会做严格兜底。
 */
export function isValidIP(input: string): boolean {
  const s = input.trim()
  if (s === '') return false

  const v4 = IPV4_RE.exec(s)
  if (v4) {
    return v4.slice(1).every((octet) => {
      if (octet.length > 1 && octet.startsWith('0')) return false
      return Number(octet) <= 255
    })
  }

  if (!s.includes(':')) return false
  if (!IPV6_CHARS_RE.test(s)) return false
  if (s.includes(':::')) return false
  const groups = s.split(':')
  // 「::」压缩写法允许 3-8 段，未压缩的完整地址固定 8 段。
  if (groups.length > 8 || groups.length < 3) return false
  if (!s.includes('::') && groups.length !== 8) return false
  return groups.every((g) => g === '' || /^[0-9a-fA-F]{1,4}$/.test(g))
}
