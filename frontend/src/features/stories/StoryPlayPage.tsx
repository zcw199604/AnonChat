import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { getWsClient } from '../../lib/ws'
import { useChat } from '../../lib/useChat'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { cancelStory, consentStory, endStory, getStory } from '../../lib/api/client'
import type { StoryDetail } from '../../lib/api/client'
import { GlassPanel } from '../../components/GlassPanel'
import { MessageList } from '../../components/MessageList'
import { Composer } from '../../components/Composer'

/** 故事剧场（how.md 2.4 暗色）：等待开演 / 演出现场 / 收录同意面板。 */
export default function StoryPlayPage() {
  const { storyId } = useParams()
  const nav = useNavigate()
  const ws = getWsClient()
  const setToast = useUiStore((s) => s.setToast)
  const [story, setStory] = useState<StoryDetail | null>(null)
  const [busy, setBusy] = useState(false)

  const conversationId = story?.conversationId ?? null
  const chat = useChat(conversationId)

  const load = useCallback(async () => {
    if (!storyId) return
    try {
      const s = await getStory(storyId)
      setStory(s)
      if (s.conversationId) ws.subscribeConversation(s.conversationId)
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }, [storyId, ws, setToast])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时拉取远端数据，状态更新在请求返回之后
    void load()
  }, [load])

  // 实时事件：开演、结束、同意状态。
  useEffect(() => {
    const offs = [
      ws.on('story.started', (d) => {
        const x = d as { storyId: string }
        if (x.storyId === storyId) void load()
      }),
      ws.on('story.ended', (d) => {
        const x = d as { storyId: string }
        if (x.storyId === storyId) void load()
      }),
      ws.on('story.consent', (d) => {
        const x = d as { storyId: string }
        if (x.storyId === storyId) void load()
      }),
    ]
    return () => offs.forEach((f) => f())
  }, [ws, storyId, load])

  async function doCancel() {
    if (!storyId) return
    setBusy(true)
    try {
      await cancelStory(storyId)
      setToast('已取消这个故事')
      nav('/stories')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    } finally {
      setBusy(false)
    }
  }

  async function doEnd() {
    if (!storyId) return
    setBusy(true)
    try {
      const s = await endStory(storyId)
      setStory(s)
      setToast(s.valid ? '演出结束，判定为有效场次' : '演出结束，未达到有效条件')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    } finally {
      setBusy(false)
    }
  }

  async function doConsent(agree: boolean) {
    if (!storyId) return
    setBusy(true)
    try {
      const s = await consentStory(storyId, agree)
      setStory(s)
      setToast(agree ? '已同意收录' : '已撤回同意')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    } finally {
      setBusy(false)
    }
  }

  if (!story) {
    return (
      <main className="flex min-h-screen items-center justify-center text-sm text-night-sub">加载中…</main>
    )
  }

  const myRoleName = story.myRole === 'host'
    ? story.hostRole === 'a'
      ? story.roleA
      : story.roleB
    : story.hostRole === 'a'
      ? story.roleB
      : story.roleA

  if (story.status === 'waiting') {
    return (
      <main className="flex min-h-screen items-center justify-center px-4 py-10">
        <div className="w-full max-w-md space-y-4 text-center">
          <h1 className="text-lg font-semibold text-night-ink">《{story.title}》</h1>
          <p className="text-sm text-night-sub">正在等待另一个人认领「{story.hostRole === 'a' ? story.roleB : story.roleA}」…</p>
          <p className="whitespace-pre-wrap rounded-card border border-glass-line bg-glass p-4 text-left text-sm text-night-ink">
            {story.opening}
          </p>
          <div className="flex justify-center gap-2">
            <button
              type="button"
              onClick={() => nav('/stories')}
              className="rounded-full border border-glass-line bg-glass px-5 py-2 text-sm text-night-ink"
            >
              返回故事大厅
            </button>
            {story.myRole === 'host' && (
              <button
                type="button"
                disabled={busy}
                onClick={doCancel}
                className="rounded-full bg-danger px-5 py-2 text-sm text-white disabled:opacity-40"
              >
                取消故事
              </button>
            )}
          </div>
        </div>
      </main>
    )
  }

  return (
    <main className="mx-auto flex h-screen max-w-[760px] flex-col p-0 md:p-4">
      <GlassPanel className="h-full rounded-none md:rounded-panel">
        <header className="flex items-center justify-between gap-2 border-b border-glass-line px-4 py-3">
          <div className="min-w-0">
            <div className="truncate text-sm font-medium text-night-ink">《{story.title}》</div>
            <div className="text-[11px] text-night-sub">
              {story.roleA} · {story.roleB}
              {story.myRole ? ` · 你是${myRoleName}` : ''}
            </div>
          </div>
          {story.status === 'active' && (
            <button
              type="button"
              disabled={busy}
              onClick={doEnd}
              className="shrink-0 rounded-sm bg-brand px-3 py-1 text-xs text-white disabled:opacity-40"
            >
              结束演出
            </button>
          )}
        </header>

        {story.status === 'active' && conversationId ? (
          <>
            <MessageList
              messages={chat.messages}
              selfMemberId={chat.myMemberId}
              typing={chat.typing}
            />
            <Composer onSend={chat.send} onTyping={chat.sendTyping} />
          </>
        ) : (
          <div className="flex-1 overflow-y-auto px-4 py-4">
            <p className="mb-3 whitespace-pre-wrap rounded-card border border-glass-line bg-glass p-4 text-sm text-night-ink">
              {story.opening}
            </p>
            {story.status === 'ended' && (
              <div className="space-y-3 rounded-card border border-glass-line bg-glass p-4">
                <div className="text-sm text-night-ink">
                  {story.valid ? '这场演出有效，可以申请收录为作品。' : '这场演出未达到有效条件（时长 5 分钟且双方各 5 条消息），不能收录。'}
                </div>
                <div className="text-xs text-night-sub">
                  发起人同意：{story.hostConsent ? '已同意' : '未同意'} · 参与者同意：
                  {story.guestConsent ? '已同意' : '未同意'}
                </div>
                {story.myRole && story.valid && (
                  <div className="flex gap-2">
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => doConsent(true)}
                      className="rounded-full bg-brand px-4 py-2 text-sm text-white disabled:opacity-40"
                    >
                      {story.myRole === 'host' ? '申请收录' : '同意收录'}
                    </button>
                    <button
                      type="button"
                      disabled={busy}
                      onClick={() => doConsent(false)}
                      className="rounded-full border border-glass-line px-4 py-2 text-sm text-night-ink disabled:opacity-40"
                    >
                      撤回同意
                    </button>
                  </div>
                )}
                <button
                  type="button"
                  onClick={() => nav('/stories')}
                  className="text-xs text-night-sub underline-offset-2 hover:underline"
                >
                  返回故事大厅
                </button>
              </div>
            )}
          </div>
        )}
      </GlassPanel>
    </main>
  )
}
