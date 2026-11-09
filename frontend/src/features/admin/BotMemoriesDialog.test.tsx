import { beforeEach, describe, expect, it, vi } from 'vitest'
import { act, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AdminBot, AdminBotMemory, AdminBotMemoryDetail } from './botApi'

const api = vi.hoisted(() => ({ list: vi.fn(), detail: vi.fn(), remove: vi.fn(), clear: vi.fn() }))
vi.mock('./botApi', () => ({
  adminBotMemories: api.list,
  adminBotMemory: api.detail,
  adminDeleteBotMemory: api.remove,
  adminClearBotMemories: api.clear,
}))
import { BotMemoriesDialog } from './BotMemoriesDialog'

const bot: AdminBot = {
  id: 'bot-1', label: '夜猫', cardId: null, mediaFolderId: null, enabled: true,
  coreMode: 'single', reasonModel: '', memoryEnabled: true, memoryCount: 21,
  model: '', temperature: 0.9, maxOutputTokens: 300, serveMatch: true, serveBottle: false,
  maxActiveMatches: 5, totalTokenLimit: 0, totalReplyLimit: 0, windowSeconds: 3600,
  windowTokenLimit: 0, windowReplyLimit: 0, convReplyLimit: 0, convMinIntervalSeconds: 0,
  convMediaLimit: 0, replyDelayMinMs: 0, replyDelayMaxMs: 0, typingMsPerChar: 0,
  bottleIntervalSeconds: 60, cardName: '', folderName: '', createdAt: '', updatedAt: '',
  profile: { nickname: '夜猫', avatarSeed: '', publicCode: '', gender: 'secret', age: null,
    province: null, provincePublic: false, tags: [], cleanMode: false },
  usage: { usedTokens: 0, usedReplies: 0, windowTokens: 0, windowPromptTokens: 0,
    windowCachedTokens: 0, windowReplies: 0, activeMatches: 0, windowReasonTokens: 0, windowMemoryTokens: 0 },
}

function memory(i: number): AdminBotMemory {
  return { identityId: `peer-${i}`, nickname: `对方${i}`, publicCode: `CODE${i}`, affinity: i,
    moodLabel: '开心', moodValue: 7, eventCount: 1, lastInteractionAt: '2026-11-09T00:00:00Z' }
}
function detail(value = '小林'): AdminBotMemoryDetail {
  return { profile: {
    basic_info: { 称呼: { v: value, t: '2026-11-09T00:00:00Z' } },
    interest: { 爱好: { v: '画画', t: '' } }, life: { 工作: { v: '上学', t: '' } },
    psychological: { 状态: { v: '轻松', t: '' } }, relationship: { 关系: { v: '熟悉', t: '' } },
    preference: { 风格: { v: '简短', t: '' } },
  }, events: [{ id: 'e1', conversationId: null, gist: '明天考试', tags: ['考试'], createdAt: '2026-11-08T00:00:00Z' }] }
}
function mount() {
  const onClose = vi.fn(), onChanged = vi.fn(), onError = vi.fn()
  render(<BotMemoriesDialog bot={bot} onClose={onClose} onChanged={onChanged} onError={onError} />)
  return { onClose, onChanged, onError }
}

beforeEach(() => {
  api.list.mockReset().mockResolvedValue([memory(1)])
  api.detail.mockReset().mockResolvedValue(detail())
  api.remove.mockReset().mockResolvedValue(undefined)
  api.clear.mockReset().mockResolvedValue(undefined)
})

describe('BotMemoriesDialog', () => {
  it('分页列出编号、关系、情绪和事件数，查看中文分组画像与事件', async () => {
    const user = userEvent.setup()
    api.list.mockResolvedValueOnce(Array.from({ length: 20 }, (_, i) => memory(i + 1))).mockResolvedValueOnce([memory(21)])
    mount()
    expect(await screen.findByText('#CODE1')).toBeInTheDocument()
    expect(screen.getAllByText('开心（7/10）')).toHaveLength(20)
    await user.click(screen.getByRole('button', { name: '下一页' }))
    expect(await screen.findByText('#CODE21')).toBeInTheDocument()
    expect(api.list).toHaveBeenLastCalledWith('bot-1', 20, 20)
    expect(screen.getByRole('button', { name: '下一页' })).toBeDisabled()
    await user.click(screen.getByRole('button', { name: '查看对方21的记忆' }))
    expect(await screen.findByText('小林')).toBeInTheDocument()
    for (const label of ['基本信息', '兴趣', '生活', '性格与状态', '和你的关系', '聊天偏好']) {
      expect(screen.getByRole('heading', { name: label })).toBeInTheDocument()
    }
    expect(screen.getByText('明天考试')).toBeInTheDocument()
    expect(api.detail).toHaveBeenCalledWith('bot-1', 'peer-21')
  })

  it('单条删除和清空都需要确认，成功后刷新列表与外部统计', async () => {
    const user = userEvent.setup()
    const { onChanged } = mount()
    await screen.findByText('#CODE1')
    await user.click(screen.getByRole('button', { name: '删除对方1的记忆' }))
    expect(api.remove).not.toHaveBeenCalled()
    let confirmation = screen.getByRole('dialog', { name: '删除这条记忆？' })
    await user.click(within(confirmation).getByRole('button', { name: '取消' }))
    expect(api.remove).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: '删除对方1的记忆' }))
    confirmation = screen.getByRole('dialog', { name: '删除这条记忆？' })
    await user.click(within(confirmation).getByRole('button', { name: '删除' }))
    await vi.waitFor(() => expect(api.remove).toHaveBeenCalledWith('bot-1', 'peer-1'))
    await vi.waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1))
    await user.click(screen.getByRole('button', { name: '清空全部记忆' }))
    expect(api.clear).not.toHaveBeenCalled()
    confirmation = screen.getByRole('dialog', { name: '清空全部记忆？' })
    await user.click(within(confirmation).getByRole('button', { name: '清空' }))
    await vi.waitFor(() => expect(api.clear).toHaveBeenCalledWith('bot-1'))
    expect(onChanged).toHaveBeenCalledTimes(2)
  })

  it('删除失败保留窗口和确认框，错误交给后台统一提示', async () => {
    const user = userEvent.setup()
    const error = new Error('删除失败')
    api.remove.mockRejectedValue(error)
    const { onError, onChanged } = mount()
    await screen.findByText('#CODE1')
    await user.click(screen.getByRole('button', { name: '删除对方1的记忆' }))
    await user.click(within(screen.getByRole('dialog', { name: '删除这条记忆？' })).getByRole('button', { name: '删除' }))
    await vi.waitFor(() => expect(onError).toHaveBeenCalledWith(error))
    expect(screen.getByRole('dialog', { name: '删除这条记忆？' })).toBeInTheDocument()
    expect(onChanged).not.toHaveBeenCalled()
  })

  it('取消确认时 ESC 只关闭确认框，末页最后一条删除后返回上一页', async () => {
    const user = userEvent.setup()
    api.list.mockResolvedValueOnce(Array.from({ length: 20 }, (_, i) => memory(i + 1)))
      .mockResolvedValueOnce([memory(21)]).mockResolvedValueOnce([memory(1)])
    const { onClose } = mount()
    await screen.findByText('#CODE1')
    await user.click(screen.getByRole('button', { name: '下一页' }))
    await screen.findByText('#CODE21')
    await user.click(screen.getByRole('button', { name: '删除对方21的记忆' }))
    await user.keyboard('{Escape}')
    expect(screen.queryByRole('dialog', { name: '删除这条记忆？' })).not.toBeInTheDocument()
    expect(onClose).not.toHaveBeenCalled()
    expect(api.remove).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: '删除对方21的记忆' }))
    await user.click(within(screen.getByRole('dialog', { name: '删除这条记忆？' })).getByRole('button', { name: '删除' }))
    expect(await screen.findByText('#CODE1')).toBeInTheDocument()
    expect(api.list).toHaveBeenLastCalledWith('bot-1', 20, 0)
    expect(screen.getByRole('button', { name: '上一页' })).toBeDisabled()
  })

  it('返回列表后切换对方时，较晚返回的旧详情不能覆盖当前详情', async () => {
    const user = userEvent.setup()
    let resolveOld!: (d: AdminBotMemoryDetail) => void
    api.list.mockResolvedValue([memory(1), memory(2)])
    api.detail.mockReturnValueOnce(new Promise<AdminBotMemoryDetail>((resolve) => { resolveOld = resolve })).mockResolvedValueOnce(detail('新对方'))
    mount()
    await screen.findByText('#CODE1')
    await user.click(screen.getByRole('button', { name: '查看对方1的记忆' }))
    await user.click(screen.getByRole('button', { name: '返回列表' }))
    await user.click(screen.getByRole('button', { name: '查看对方2的记忆' }))
    expect(await screen.findByText('新对方')).toBeInTheDocument()
    await act(async () => resolveOld(detail('旧对方')))
    expect(screen.queryByText('旧对方')).not.toBeInTheDocument()
    expect(screen.getByText('新对方')).toBeInTheDocument()
  })
})
