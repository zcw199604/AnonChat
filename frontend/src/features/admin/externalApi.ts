/** 外站管理接口仅由后台页面导入，随后台分包加载。 */
import { apiRequest } from '../../lib/api/client'

export interface ExternalSettings {
  enabled: boolean
  chat2019Enabled: boolean
  wanfengEnabled: boolean
  onlineThreshold: number
  localWaitSeconds: number
  externalMatchSeconds: number
  chat2019MaxSessions: number
  wanfengMaxSessions: number
}

export interface ExternalProviderStatus {
  name: string
  enabled: boolean
  available: boolean
  reason: string
  links: number
  matching: number
  inRoom: number
  idle: number
  cooldownUntil: string
  lastError: string
  lastErrorAt: string
  counters: Record<string, number>
}

export interface ExternalStatus {
  providers: ExternalProviderStatus[]
  online: number
  queue: number
}

export function adminExternalSettings(): Promise<ExternalSettings> {
  return apiRequest('GET', '/api/admin/external/settings')
}

export function adminSaveExternalSettings(body: ExternalSettings): Promise<ExternalSettings> {
  return apiRequest('PUT', '/api/admin/external/settings', body)
}

export function adminExternalStatus(): Promise<ExternalStatus> {
  return apiRequest('GET', '/api/admin/external/status')
}
