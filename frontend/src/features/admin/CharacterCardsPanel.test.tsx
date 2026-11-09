import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AdminBot, CharacterCard } from './botApi'

const adminCharacterCards = vi.fn()
const adminBots = vi.fn()
const adminCreateCharacterCard = vi.fn()
const adminUpdateCharacterCard = vi.fn()
const adminDeleteCharacterCard = vi.fn()
const adminImportCharacterCard = vi.fn()
const adminCardExpressions = vi.fn()
const adminCreateCardExpression = vi.fn()
const adminUpdateCardExpression = vi.fn()
const adminDeleteCardExpression = vi.fn()

vi.mock('./botApi', () => ({
  adminImportCharacterCard: (file: File) => adminImportCharacterCard(file),
  adminCardExpressions: (id: string) => adminCardExpressions(id),
  adminCreateCardExpression: (...args: unknown[]) => adminCreateCardExpression(...args),
  adminUpdateCardExpression: (...args: unknown[]) => adminUpdateCardExpression(...args),
  adminDeleteCardExpression: (id: string) => adminDeleteCardExpression(id),
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
  heartPrompt: '心核原文', reasonPrompt: '智核原文', alternateGreetings: ['备选原文'],
  postHistoryInstructions: '历史后指令', lorebook: [{ keys: ['考试'], content: '学校', enabled: true, constant: false, case_sensitive: false, order: 1, comment: '' }],
  tools: ['recall_memory'], creatorNotes: '作者备注', sourceFormat: 'krp', expressionCount: 1,
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
    adminImportCharacterCard.mockReset().mockResolvedValue({ card, warnings: ['已忽略上传代码'] })
    adminCardExpressions.mockReset().mockResolvedValue([{ id: 'e1', key: 'happy', description: '开心', mediaItemId: 'm1', sort: 0 }])
    adminCreateCardExpression.mockReset().mockResolvedValue({ id: 'e2', key: 'sad', description: '难过', mediaItemId: 'm2', sort: 0 })
    adminUpdateCardExpression.mockReset().mockResolvedValue({ id: 'e1', key: 'smile', description: '微笑', mediaItemId: 'm1', sort: 2 })
    adminDeleteCardExpression.mockReset().mockResolvedValue(undefined)
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
  it('导入支持四种格式，成功刷新并展示 warnings 和来源、表情数', async () => {
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={vi.fn()} />)
    const tile = await screen.findByTestId('admin-card')
    expect(tile).toHaveTextContent('KRP')
    expect(tile).toHaveTextContent('1 个表情')
    const input = screen.getByLabelText('导入角色卡文件')
    expect(input).toHaveAttribute('accept', '.json,.png,.zip,.krp')
    const file = new File(['{}'], 'card.json', { type: 'application/json' })
    await user.upload(input, file)
    expect(adminImportCharacterCard).toHaveBeenCalledWith(file)
    expect(await screen.findByRole('dialog', { name: '导入结果' })).toHaveTextContent('已忽略上传代码')
    await vi.waitFor(() => expect(adminCharacterCards).toHaveBeenCalledTimes(2))
  })

  it('扩展字段分区编辑，世界书关键词保留输入分隔符，备选开场白和条目可增删', async () => {
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={vi.fn()} />)
    await screen.findByTestId('admin-card')
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = screen.getByRole('dialog', { name: '编辑角色卡' })
    for (const title of ['基础', '双核', '开场白', '世界书', '工具与表情']) {
      expect(within(dialog).getByRole('heading', { name: title })).toBeInTheDocument()
    }
    expect(within(dialog).getByText('全部不勾选 = 全部启用')).toBeInTheDocument()
    await user.type(within(dialog).getByLabelText('心核人设'), '扩展')
    await user.click(within(dialog).getByRole('button', { name: '添加备选开场白' }))
    await user.type(within(dialog).getByLabelText('备选开场白 2'), '新的开场')
    await user.click(within(dialog).getByRole('button', { name: '删除备选开场白 1' }))
    await user.click(within(dialog).getByRole('button', { name: '添加世界书条目' }))
    await user.type(within(dialog).getByLabelText('关键词 2'), '音乐,考试')
    await user.type(within(dialog).getByLabelText('内容 2'), '爱听歌')
    await user.click(within(dialog).getByLabelText('常驻 2'))
    await user.click(within(dialog).getByLabelText('大小写敏感 2'))
    await user.click(within(dialog).getByRole('button', { name: '删除世界书条目 1' }))
    await user.click(within(dialog).getByLabelText('回忆记忆'))
    await user.click(within(dialog).getByRole('button', { name: '保存' }))
    await vi.waitFor(() => expect(adminUpdateCharacterCard).toHaveBeenCalledWith('c1', expect.objectContaining({
      heartPrompt: '心核原文扩展', reasonPrompt: '智核原文', alternateGreetings: ['新的开场'],
      postHistoryInstructions: '历史后指令', creatorNotes: '作者备注', tools: [],
      lorebook: [expect.objectContaining({ keys: ['音乐', '考试'], content: '爱听歌', constant: true, case_sensitive: true })],
    })))
  })

  it('复制带上新字段但不复制表情，未保存的卡不显示表情管理', async () => {
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={vi.fn()} />)
    await screen.findByTestId('admin-card')
    await user.click(screen.getByRole('button', { name: '复制' }))
    const dialog = screen.getByRole('dialog', { name: '复制角色卡' })
    expect(dialog).toHaveTextContent('不复制表情')
    expect(within(dialog).queryByLabelText('表情图片')).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: '创建' }))
    await vi.waitFor(() => expect(adminCreateCharacterCard).toHaveBeenCalledWith(expect.objectContaining({
      heartPrompt: '心核原文', reasonPrompt: '智核原文', alternateGreetings: ['备选原文'],
      creatorNotes: '作者备注', tools: ['recall_memory'], lorebook: card.lorebook,
    })))
    const sent = adminCreateCharacterCard.mock.calls[0][0]
    expect(sent).not.toHaveProperty('sourceFormat')
    expect(sent).not.toHaveProperty('expressionCount')
  })

  it('已保存角色卡可预览、修改、上传和删除表情', async () => {
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={vi.fn()} />)
    await screen.findByTestId('admin-card')
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = screen.getByRole('dialog', { name: '编辑角色卡' })
    expect(await within(dialog).findByAltText('表情 happy')).toHaveAttribute('src', '/api/admin/media-items/m1/file')
    await user.clear(within(dialog).getByLabelText('表情标识 happy'))
    await user.type(within(dialog).getByLabelText('表情标识 happy'), 'smile')
    await user.clear(within(dialog).getByLabelText('表情说明 happy'))
    await user.type(within(dialog).getByLabelText('表情说明 happy'), '微笑')
    await user.clear(within(dialog).getByLabelText('表情排序 happy'))
    await user.type(within(dialog).getByLabelText('表情排序 happy'), '2')
    await user.click(within(dialog).getByRole('button', { name: '保存表情 happy' }))
    await vi.waitFor(() => expect(adminUpdateCardExpression).toHaveBeenCalledWith('e1', { key: 'smile', description: '微笑', sort: 2 }))
    const image = new File(['image'], 'sad.png', { type: 'image/png' })
    await user.upload(within(dialog).getByLabelText('表情图片'), image)
    await user.type(within(dialog).getByLabelText('新表情标识'), 'sad')
    await user.type(within(dialog).getByLabelText('新表情说明'), '难过')
    await user.click(within(dialog).getByRole('button', { name: '上传表情' }))
    await vi.waitFor(() => expect(adminCreateCardExpression).toHaveBeenCalledWith('c1', image, 'sad', '难过'))
    await user.click(within(dialog).getByRole('button', { name: '删除表情 smile' }))
    await vi.waitFor(() => expect(adminDeleteCardExpression).toHaveBeenCalledWith('e1'))
  })

  it('导入失败保留现有卡片，不显示成功结果', async () => {
    const failure = new Error('文件损坏')
    adminImportCharacterCard.mockRejectedValue(failure)
    const onError = vi.fn()
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={onError} />)
    await screen.findByTestId('admin-card')
    await user.upload(screen.getByLabelText('导入角色卡文件'), new File(['bad'], 'bad.json', { type: 'application/json' }))
    await vi.waitFor(() => expect(onError).toHaveBeenCalledWith(failure))
    expect(screen.queryByRole('dialog', { name: '导入结果' })).not.toBeInTheDocument()
    expect(screen.getByTestId('admin-card')).toHaveTextContent('小雨')
  })

  it('表情修改失败保留输入，进行中禁止关闭与提交角色卡', async () => {
    let reject!: (error: Error) => void
    adminUpdateCardExpression.mockImplementation(() => new Promise((_, fail) => { reject = fail }))
    const onError = vi.fn()
    const user = userEvent.setup()
    render(<CharacterCardsPanel onError={onError} />)
    await screen.findByTestId('admin-card')
    await user.click(screen.getByRole('button', { name: '编辑' }))
    const dialog = screen.getByRole('dialog', { name: '编辑角色卡' })
    const input = await within(dialog).findByLabelText('表情标识 happy')
    await user.clear(input)
    await user.type(input, '保留输入')
    await user.click(within(dialog).getByRole('button', { name: '保存表情 happy' }))
    expect(within(dialog).getByRole('button', { name: '保存' })).toBeDisabled()
    expect(within(dialog).getByRole('button', { name: '取消' })).toBeDisabled()
    await user.keyboard('{Escape}')
    expect(screen.getByRole('dialog', { name: '编辑角色卡' })).toBeInTheDocument()
    reject(new Error('更新失败'))
    await vi.waitFor(() => expect(onError).toHaveBeenCalled())
    expect(input).toHaveValue('保留输入')
    expect(within(dialog).getByRole('button', { name: '保存' })).toBeEnabled()
  })

})
