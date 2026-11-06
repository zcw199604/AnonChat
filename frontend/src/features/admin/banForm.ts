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
