import { useEffect, useState } from 'react'
import {
  adminBotMemories,
  adminBotMemory,
  adminClearBotMemories,
  adminDeleteBotMemory,
} from './botApi'
import type { AdminBot, AdminBotMemory, AdminBotMemoryDetail } from './botApi'
import { formatTime } from './labels'
import { Button, ConfirmDialog, EmptyState, LoadingRows, Modal } from './ui'

const PAGE_SIZE = 20
const MEMORY_TOPICS = [
  ['basic_info', '基本信息'],
  ['interest', '兴趣'],
  ['life', '生活'],
  ['psychological', '性格与状态'],
  ['relationship', '和你的关系'],
  ['preference', '聊天偏好'],
] as const

type Confirmation = { kind: 'delete'; peer: AdminBotMemory } | { kind: 'clear' }

type Props = {
  bot: AdminBot
  onClose: () => void
  onChanged: () => void
  onError: (e: unknown) => void
}

/** 记忆管理复用后台弹窗；只在查看某位对方时读取画像和事件。 */
export function BotMemoriesDialog({ bot, onClose, onChanged, onError }: Props) {
  const [offset, setOffset] = useState(0)
  const [revision, setRevision] = useState(0)
  const [list, setList] = useState<{ rows: AdminBotMemory[]; loading: boolean; failed: boolean }>({ rows: [], loading: true, failed: false })
  const [selected, setSelected] = useState<AdminBotMemory | null>(null)
  const [detail, setDetail] = useState<{ value: AdminBotMemoryDetail | null; loading: boolean; failed: boolean }>({ value: null, loading: false, failed: false })
  const [confirm, setConfirm] = useState<Confirmation | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    let active = true
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 分页或删除后重新读取，随后由异步请求填充列表
    setList((old) => ({ ...old, loading: true, failed: false }))
    adminBotMemories(bot.id, PAGE_SIZE, offset).then((rows) => {
      if (active) setList({ rows, loading: false, failed: false })
    }).catch((e: unknown) => {
      if (!active) return
      setList((old) => ({ ...old, loading: false, failed: true }))
      onError(e)
    })
    return () => { active = false }
  }, [bot.id, offset, revision, onError])

  useEffect(() => {
    if (!selected) return
    let active = true
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 选择对方后重置旧详情，避免展示上一位的画像
    setDetail({ value: null, loading: true, failed: false })
    adminBotMemory(bot.id, selected.identityId).then((value) => {
      if (active) setDetail({ value, loading: false, failed: false })
    }).catch((e: unknown) => {
      if (!active) return
      setDetail({ value: null, loading: false, failed: true })
      onError(e)
    })
    return () => { active = false }
  }, [bot.id, selected, revision, onError])

  async function runConfirm() {
    if (!confirm) return
    setBusy(true)
    try {
      if (confirm.kind === 'delete') {
        await adminDeleteBotMemory(bot.id, confirm.peer.identityId)
        // 删除当前页最后一条时回到上一页，避免停留在空页。
        if (offset > 0 && list.rows.length === 1) setOffset((old) => Math.max(0, old - PAGE_SIZE))
      } else {
        await adminClearBotMemories(bot.id)
        setOffset(0)
      }
      setSelected(null)
      setConfirm(null)
      setRevision((old) => old + 1)
      onChanged()
    } catch (e) {
      onError(e)
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      <Modal
        open
        size="xl"
        title={`「${bot.label}」的记忆`}
        description="画像和事件仅供后台管理查看。"
        closeDisabled={confirm !== null || busy}
        onClose={onClose}
        footer={<Button variant="ghost" disabled={confirm !== null || busy} onClick={onClose}>关闭</Button>}
      >
        <div className="space-y-4 pb-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            {selected ? (
              <Button size="sm" variant="ghost" onClick={() => setSelected(null)}>返回列表</Button>
            ) : <span className="text-xs text-lobby-sub">第 {offset / PAGE_SIZE + 1} 页 · 每页 {PAGE_SIZE} 条</span>}
            <Button size="sm" variant="danger" disabled={busy || list.loading || (list.rows.length === 0 && offset === 0)} onClick={() => setConfirm({ kind: 'clear' })}>清空全部记忆</Button>
          </div>

          {selected ? (
            <section className="space-y-3" aria-label="记忆详情">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h3 className="text-sm font-semibold text-lobby-ink">{selected.nickname} <span className="font-mono text-xs text-lobby-sub">#{selected.publicCode}</span></h3>
                  <p className="mt-1 text-xs text-lobby-sub">好感度 {selected.affinity} · {selected.moodLabel || '平静'}（{selected.moodValue}/10）</p>
                </div>
                <Button size="sm" variant="ghost" className="text-danger" onClick={() => setConfirm({ kind: 'delete', peer: selected })}>删除这条记忆</Button>
              </div>
              {detail.loading ? <LoadingRows rows={3} /> : detail.failed ? (
                <div className="space-y-2 text-sm text-danger">详情加载失败。<Button size="sm" onClick={() => setRevision((old) => old + 1)}>重试</Button></div>
              ) : detail.value ? <MemoryDetail value={detail.value} /> : null}
            </section>
          ) : list.loading ? <LoadingRows rows={4} /> : list.failed ? (
            <div className="space-y-2 text-sm text-danger">记忆加载失败。<Button size="sm" onClick={() => setRevision((old) => old + 1)}>重试</Button></div>
          ) : (
            <>
              {list.rows.length === 0 ? <EmptyState>暂无记忆。</EmptyState> : (
                <ul className="space-y-2">
                  {list.rows.map((peer) => (
                    <li key={peer.identityId} className="rounded-card border border-lobby-line p-3">
                      <div className="flex flex-wrap items-start justify-between gap-2">
                        <div className="min-w-0 space-y-1">
                          <div className="break-words text-sm font-medium text-lobby-ink">{peer.nickname} <span className="font-mono text-xs text-lobby-sub">#{peer.publicCode}</span></div>
                          <div className="text-xs text-lobby-sub">好感度 {peer.affinity} · <span>{peer.moodLabel || '平静'}（{peer.moodValue}/10）</span> · {peer.eventCount} 条事件</div>
                          <div className="text-[11px] text-lobby-sub">最后互动：{formatTime(peer.lastInteractionAt)}</div>
                        </div>
                        <div className="flex gap-1">
                          <Button size="sm" variant="ghost" aria-label={`查看${peer.nickname}的记忆`} onClick={() => setSelected(peer)}>查看</Button>
                          <Button size="sm" variant="ghost" className="text-danger" aria-label={`删除${peer.nickname}的记忆`} onClick={() => setConfirm({ kind: 'delete', peer })}>删除</Button>
                        </div>
                      </div>
                    </li>
                  ))}
                </ul>
              )}
              <div className="flex justify-between gap-2">
                <Button size="sm" disabled={offset === 0} onClick={() => setOffset((old) => Math.max(0, old - PAGE_SIZE))}>上一页</Button>
                <Button size="sm" disabled={list.rows.length < PAGE_SIZE} onClick={() => setOffset((old) => old + PAGE_SIZE)}>下一页</Button>
              </div>
            </>
          )}
        </div>
      </Modal>
      <ConfirmDialog
        open={confirm !== null}
        title={confirm?.kind === 'delete' ? '删除这条记忆？' : '清空全部记忆？'}
        description={confirm?.kind === 'delete' ? `将删除对「${confirm.peer.nickname}」的画像和全部事件，删除后不可恢复。` : `将删除「${bot.label}」的全部画像和事件，删除后不可恢复。`}
        confirmText={confirm?.kind === 'delete' ? '删除' : '清空'}
        danger
        onConfirm={runConfirm}
        onClose={() => setConfirm(null)}
      />
    </>
  )
}

function MemoryDetail({ value }: { value: AdminBotMemoryDetail }) {
  const topics = MEMORY_TOPICS.filter(([key]) => Object.keys(value.profile[key] ?? {}).length > 0)
  return (
    <div className="space-y-4">
      {topics.length === 0 ? <EmptyState>暂无画像。</EmptyState> : topics.map(([key, label]) => (
        <section key={key} className="space-y-2 rounded-card border border-lobby-line p-3">
          <h4 className="text-xs font-semibold text-lobby-ink">{label}</h4>
          <dl className="space-y-2 text-sm">
            {Object.entries(value.profile[key]).map(([subtopic, entry]) => (
              <div key={subtopic} className="grid gap-1 sm:grid-cols-[7rem_1fr]">
                <dt className="break-words text-lobby-sub">{subtopic}</dt>
                <dd className="whitespace-pre-wrap break-words text-lobby-ink">{entry.v}</dd>
              </div>
            ))}
          </dl>
        </section>
      ))}
      <section className="space-y-2">
        <h4 className="text-xs font-semibold text-lobby-ink">事件时间线</h4>
        {value.events.length === 0 ? <EmptyState>暂无事件。</EmptyState> : (
          <ol className="space-y-2">
            {value.events.map((event) => (
              <li key={event.id} className="space-y-1 rounded-card border border-lobby-line p-3 text-sm">
                <div className="text-[11px] text-lobby-sub">{formatTime(event.createdAt)}</div>
                <p className="whitespace-pre-wrap break-words text-lobby-ink">{event.gist}</p>
                {event.tags.length > 0 && <p className="text-xs text-lobby-sub">{event.tags.join('、')}</p>}
              </li>
            ))}
          </ol>
        )}
      </section>
    </div>
  )
}
