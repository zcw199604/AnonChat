import type { ExternalSettings } from './externalApi'

export type ExternalForm = Omit<ExternalSettings,
  'onlineThreshold' | 'localWaitSeconds' | 'externalMatchSeconds' | 'chat2019MaxSessions' | 'wanfengMaxSessions'
> & {
  onlineThreshold: string
  localWaitSeconds: string
  externalMatchSeconds: string
  chat2019MaxSessions: string
  wanfengMaxSessions: string
}

// 与后端 UpdateInput.Validate 保持一致。
export const EXTERNAL_FIELDS = [
  { key: 'onlineThreshold', label: '在线人数阈值', min: 0, max: 100000 },
  { key: 'localWaitSeconds', label: '本站等待（秒）', min: 0, max: 600 },
  { key: 'externalMatchSeconds', label: '外站匹配时长（秒）', min: 3, max: 120 },
  { key: 'chat2019MaxSessions', label: 'chat2019 最大连接数', min: 1, max: 1000 },
  { key: 'wanfengMaxSessions', label: '晚风最大连接数', min: 1, max: 1000 },
] as const

export function externalToForm(s: ExternalSettings): ExternalForm {
  return {
    ...s,
    onlineThreshold: String(s.onlineThreshold),
    localWaitSeconds: String(s.localWaitSeconds),
    externalMatchSeconds: String(s.externalMatchSeconds),
    chat2019MaxSessions: String(s.chat2019MaxSessions),
    wanfengMaxSessions: String(s.wanfengMaxSessions),
  }
}

export function validateExternalForm(f: ExternalForm): string[] {
  return EXTERNAL_FIELDS.flatMap(({ key, label, min, max }) => {
    const raw = f[key].trim()
    const n = Number(raw)
    return raw === '' || !Number.isSafeInteger(n) || n < min || n > max
      ? [`${label}需为 ${min}–${max} 之间的整数`]
      : []
  })
}

export function externalPayload(f: ExternalForm): ExternalSettings {
  return {
    ...f,
    onlineThreshold: Number(f.onlineThreshold),
    localWaitSeconds: Number(f.localWaitSeconds),
    externalMatchSeconds: Number(f.externalMatchSeconds),
    chat2019MaxSessions: Number(f.chat2019MaxSessions),
    wanfengMaxSessions: Number(f.wanfengMaxSessions),
  }
}
