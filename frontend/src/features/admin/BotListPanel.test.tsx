import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AdminBot } from './botApi'

const adminBots = vi.fn()
const adminCreateBot = vi.fn()
const adminUpdateBot = vi.fn()
const adminDeleteBot = vi.fn()
const adminResetBotUsage = vi.fn()

vi.mock('./botApi', () => ({
  adminBots: () => adminBots(),
  adminCharacterCards: () => Promise.resolve([]),
  adminMediaFolders: () => Promise.resolve([]),
  adminCreateBot: (b: unknown) => adminCreateBot(b),
  adminUpdateBot: (id: string, b: unknown) => adminUpdateBot(id, b),
  adminDeleteBot: (id: string) => adminDeleteBot(id),
  adminResetBotUsage: (id: string) => adminResetBotUsage(id),
}))

import { BotListPanel } from './BotListPanel'

function sampleBot(over: Partial<AdminBot> = {}): AdminBot {
  return {
    id: 'bot-1',
    label: '夜猫',
    cardId: 'card-1',
    mediaFolderId: null,
    enabled: true,
    model: '',
    temperature: 0.9,
    maxOutputTokens: 300,
    serveMatch: true,
    serveBottle: false,
    maxActiveMatches: 5,
    totalTokenLimit: 1000,
    totalReplyLimit: 0,
    windowSeconds: 3600,
    windowTokenLimit: 0,
    windowReplyLimit: 10,
    convReplyLimit: 3,
    convMinIntervalSeconds: 5,
    convMediaLimit: 2,
    replyDelayMinMs: 3000,
    replyDelayMaxMs: 12000,
    typingMsPerChar: 180,
    bottleIntervalSeconds: 1800,
    cardName: '小雨',
    folderName: '',
    profile: {
      nickname: '小雨同学',
      avatarSeed: 'seed',
      publicCode: 'ABCD123456',
      gender: 'female',
      age: 22,
      province: '浙江',
      provincePublic: true,
      tags: ['音乐'],
      cleanMode: false,
    },
    usage: {
      usedTokens: 120,
      usedReplies: 2,
      windowTokens: 120,
      windowPromptTokens: 100,
      windowCachedTokens: 60,
      windowReplies: 2,
      activeMatches: 1,
    },
    createdAt: '2026-11-06T00:00:00Z',
    updatedAt: '2026-11-06T00:00:00Z',
    ...over,
  }
}

describe('BotListPanel', () => {
  beforeEach(() => {
    adminBots.mockReset().mockResolvedValue([sampleBot(), sampleBot({ id: 'bot-2', label: '早鸟', enabled: false })])
    adminCreateBot.mockReset().mockResolvedValue(undefined)
    adminUpdateBot.mockReset().mockResolvedValue(undefined)
    adminDeleteBot.mockReset().mockResolvedValue(undefined)
    adminResetBotUsage.mockReset().mockResolvedValue(undefined)
  })

  it('每个 bot 渲染为一张卡片，显示状态、角色卡与用量', async () => {
    render(<BotListPanel onError={vi.fn()} />)
    const tiles = await screen.findAllByTestId('admin-bot')
    expect(tiles).toHaveLength(2)
    expect(tiles[0]).toHaveTextContent('夜猫')
    expect(tiles[0]).toHaveTextContent('已启用')
    expect(tiles[0]).toHaveTextContent('小雨')
    expect(tiles[0]).toHaveTextContent('120 / 1000')
    expect(tiles[1]).toHaveTextContent('已停用')
    expect(screen.getByText(/共 2 个 bot · 已启用 1 个/)).toBeInTheDocument()
  })

  it('「新建 Bot」在弹窗中创建', async () => {
    const user = userEvent.setup()
    render(<BotListPanel onError={vi.fn()} />)
    await screen.findAllByTestId('admin-bot')
    expect(screen.queryByLabelText('对外昵称')).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: '新建 Bot' }))
    const dialog = screen.getByRole('dialog', { name: '新建 Bot' })
    await user.type(within(dialog).getByLabelText('备注名'), '新 bot')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    await vi.waitFor(() => expect(adminCreateBot).toHaveBeenCalledWith(expect.objectContaining({ label: '新 bot' })))
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('编辑在弹窗中保存', async () => {
    const user = userEvent.setup()
    render(<BotListPanel onError={vi.fn()} />)
    const [first] = await screen.findAllByTestId('admin-bot')

    await user.click(within(first).getByRole('button', { name: '编辑' }))
    const dialog = screen.getByRole('dialog', { name: '编辑 bot「夜猫」' })
    const label = within(dialog).getByLabelText('备注名')
    await user.clear(label)
    await user.type(label, '夜猫 2')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    await vi.waitFor(() => expect(adminUpdateBot).toHaveBeenCalledWith('bot-1', expect.objectContaining({ label: '夜猫 2' })))
  })

  it('删除需要确认', async () => {
    const user = userEvent.setup()
    render(<BotListPanel onError={vi.fn()} />)
    const [first] = await screen.findAllByTestId('admin-bot')

    await user.click(within(first).getByRole('button', { name: '删除' }))
    const dialog = screen.getByRole('dialog')
    expect(adminDeleteBot).not.toHaveBeenCalled()
    await user.click(within(dialog).getByRole('button', { name: '删除' }))
    await vi.waitFor(() => expect(adminDeleteBot).toHaveBeenCalledWith('bot-1'))
  })
})
