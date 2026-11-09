import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AdminBot, CharacterCard } from './botApi'

const adminCharacterCards = vi.fn()
const adminBots = vi.fn()
const adminCreateCharacterCard = vi.fn()
const adminUpdateCharacterCard = vi.fn()
const adminDeleteCharacterCard = vi.fn()

vi.mock('./botApi', () => ({
  adminCharacterCards: () => adminCharacterCards(),
  adminBots: () => adminBots(),
  adminCreateCharacterCard: (b: unknown) => adminCreateCharacterCard(b),
  adminUpdateCharacterCard: (id: string, b: unknown) => adminUpdateCharacterCard(id, b),
  adminDeleteCharacterCard: (id: string) => adminDeleteCharacterCard(id),
}))

import { CharacterCardsPanel } from './CharacterCardsPanel'

const card: CharacterCard = {
  id: 'c1',
  name: '小雨',
  description: '大学生，短发',
  personality: '温柔、爱用语气词',
  scenario: '深夜睡不着',
  firstMessage: '你也还没睡呀',
  exampleDialogue: '',
  systemPrompt: '',
  createdAt: '2026-01-01T00:00:00Z',
  updatedAt: '2026-01-02T00:00:00Z',
}

describe('CharacterCardsPanel', () => {
  beforeEach(() => {
    adminCharacterCards.mockReset().mockResolvedValue([card])
    adminBots.mockReset().mockResolvedValue([{ id: 'b1', label: '夜猫', cardId: 'c1' } as AdminBot])
    adminCreateCharacterCard.mockReset().mockResolvedValue({ ...card, id: 'c2' })
    adminUpdateCharacterCard.mockReset().mockResolvedValue(card)
    adminDeleteCharacterCard.mockReset().mockResolvedValue(undefined)
  })

  it('以卡片展示角色卡，新建表单默认不出现在页面上', async () => {
    render(<CharacterCardsPanel onError={vi.fn()} />)
    const tile = await screen.findByTestId('admin-card')
    expect(tile).toHaveTextContent('小雨')
    expect(tile).toHaveTextContent('1 个 bot 在用')
    expect(screen.queryByLabelText('角色名')).not.toBeInTheDocument()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

  it('点击「新建角色卡」弹窗创建', async () => {
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={vi.fn()} />)
    await screen.findByTestId('admin-card')

    await user.click(screen.getByRole('button', { name: '新建角色卡' }))
    const dialog = screen.getByRole('dialog', { name: '新建角色卡' })
    await user.type(within(dialog).getByLabelText('角色名'), '阿杰')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))

    await vi.waitFor(() =>
      expect(adminCreateCharacterCard).toHaveBeenCalledWith(expect.objectContaining({ name: '阿杰', personality: '' })),
    )
    await vi.waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  })

  it('角色名为空时不提交', async () => {
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={vi.fn()} />)
    await screen.findByTestId('admin-card')
    await user.click(screen.getByRole('button', { name: '新建角色卡' }))
    const dialog = screen.getByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    expect(within(dialog).getByText('角色名不能为空')).toBeInTheDocument()
    expect(adminCreateCharacterCard).not.toHaveBeenCalled()
  })

  it('复制：预填原卡内容与「副本」名称，保存为新卡', async () => {
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={vi.fn()} />)
    const tile = await screen.findByTestId('admin-card')

    await user.click(within(tile).getByRole('button', { name: '复制' }))
    const dialog = screen.getByRole('dialog', { name: '复制角色卡' })
    expect(within(dialog).getByLabelText('角色名')).toHaveValue('小雨 副本')
    expect(within(dialog).getByLabelText('性格与说话风格')).toHaveValue('温柔、爱用语气词')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))

    await vi.waitFor(() =>
      expect(adminCreateCharacterCard).toHaveBeenCalledWith(
        expect.objectContaining({ name: '小雨 副本', scenario: '深夜睡不着', firstMessage: '你也还没睡呀' }),
      ),
    )
    expect(adminUpdateCharacterCard).not.toHaveBeenCalled()
  })

  it('编辑：保存时更新原卡', async () => {
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={vi.fn()} />)
    const tile = await screen.findByTestId('admin-card')

    await user.click(within(tile).getByRole('button', { name: '编辑' }))
    const dialog = screen.getByRole('dialog', { name: '编辑角色卡' })
    const scenario = within(dialog).getByLabelText('场景设定')
    await user.clear(scenario)
    await user.type(scenario, '下雨天')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await vi.waitFor(() =>
      expect(adminUpdateCharacterCard).toHaveBeenCalledWith('c1', expect.objectContaining({ name: '小雨', scenario: '下雨天' })),
    )
    expect(adminCreateCharacterCard).not.toHaveBeenCalled()
  })

  it('保存失败时弹窗保持打开，已填内容不丢', async () => {
    adminCreateCharacterCard.mockRejectedValue(new Error('boom'))
    const onError = vi.fn()
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={onError} />)
    await screen.findByTestId('admin-card')

    await user.click(screen.getByRole('button', { name: '新建角色卡' }))
    const dialog = screen.getByRole('dialog')
    await user.type(within(dialog).getByLabelText('角色名'), '阿杰')
    await user.click(within(dialog).getByRole('button', { name: '创建' }))

    await vi.waitFor(() => expect(onError).toHaveBeenCalled())
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    expect(within(dialog).getByLabelText('角色名')).toHaveValue('阿杰')
  })

  it('删除前确认，并提示正在使用它的 bot', async () => {
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={vi.fn()} />)
    const tile = await screen.findByTestId('admin-card')

    await user.click(within(tile).getByRole('button', { name: '删除' }))
    const dialog = screen.getByRole('dialog')
    expect(dialog).toHaveTextContent('夜猫')
    await user.click(within(dialog).getByRole('button', { name: '删除' }))
    await vi.waitFor(() => expect(adminDeleteCharacterCard).toHaveBeenCalledWith('c1'))
  })
})
