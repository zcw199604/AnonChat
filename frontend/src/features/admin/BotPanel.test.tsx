import { beforeEach, describe, expect, it, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { BotSettings, BotSettingsInput } from './botApi'

const getSettings = vi.fn()
const saveSettings = vi.fn()
vi.mock('./botApi', () => ({
  adminBotSettings: () => getSettings(),
  adminSaveBotSettings: (body: BotSettingsInput) => saveSettings(body),
}))
vi.mock('./BotListPanel', () => ({ BotListPanel: () => null }))
vi.mock('./CharacterCardsPanel', () => ({ CharacterCardsPanel: () => null }))

import { BotPanel } from './BotPanel'

const sample: BotSettings = {
  enabled: true, baseUrl: '', model: 'heart', timeoutSeconds: 30, sendCacheKey: true,
  matchFallbackSeconds: 3, bottleMinAgeSeconds: 60, timezone: 'Asia/Shanghai', personaMode: 'user',
  reasonModel: 'reason-default', memoryModel: 'memory-default', memoryRetentionDays: 30,
  memoryExtractEvery: 20, oocGuard: true,
  apiKeySet: false, apiKeyHint: '', apiKeySource: 'none', secretStorageAvailable: true,
  ready: false, notReadyReason: '尚未配置', updatedAt: '',
}

describe('BotPanel 全局设置', () => {
  beforeEach(() => {
    getSettings.mockReset().mockResolvedValue(sample)
    saveSettings.mockReset().mockImplementation((body) => Promise.resolve({ ...sample, ...body }))
  })

  it('读取并保存智核、记忆和跑偏检测设置', async () => {
    const user = userEvent.setup()
    render(<BotPanel onError={vi.fn()} />)
    expect(await screen.findByLabelText('智核默认模型')).toHaveValue('reason-default')
    expect(screen.getByLabelText('记忆模型')).toHaveValue('memory-default')
    expect(screen.getByLabelText('记忆保留天数')).toHaveValue(30)
    expect(screen.getByLabelText('记忆抽取间隔')).toHaveValue(20)
    await user.clear(screen.getByLabelText('智核默认模型'))
    await user.type(screen.getByLabelText('智核默认模型'), 'new-reason')
    await user.clear(screen.getByLabelText('记忆模型'))
    await user.type(screen.getByLabelText('记忆模型'), 'new-memory')
    await user.clear(screen.getByLabelText('记忆保留天数'))
    await user.type(screen.getByLabelText('记忆保留天数'), '365')
    await user.clear(screen.getByLabelText('记忆抽取间隔'))
    await user.type(screen.getByLabelText('记忆抽取间隔'), '200')
    await user.click(screen.getByRole('switch', { name: '跑偏检测' }))
    await user.click(screen.getByRole('button', { name: '保存' }))
    await vi.waitFor(() => expect(saveSettings).toHaveBeenCalledWith(expect.objectContaining({
      reasonModel: 'new-reason', memoryModel: 'new-memory', memoryRetentionDays: 365,
      memoryExtractEvery: 200, oocGuard: false,
    })))
  })

  it('保留期或抽取间隔越界时不提交', async () => {
    const user = userEvent.setup()
    render(<BotPanel onError={vi.fn()} />)
    const days = await screen.findByLabelText('记忆保留天数')
    await user.clear(days)
    await user.type(days, '366')
    await user.clear(screen.getByLabelText('记忆抽取间隔'))
    await user.type(screen.getByLabelText('记忆抽取间隔'), '4')
    await user.click(screen.getByRole('button', { name: '保存' }))
    expect(saveSettings).not.toHaveBeenCalled()
    expect(screen.getByText(/记忆保留天数需为/)).toBeInTheDocument()
  })
})
