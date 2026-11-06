import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { getWsClient } from '../../lib/ws'
import { errorMessage } from '../../lib/api/errorMessages'
import { useUiStore } from '../../stores/ui'
import { postBottle, pickBottle, throwBackBottle, replyBottle, myBottles, reportBottle } from '../../lib/api/client'
import type { PickedBottle, MyBottle } from '../../lib/api/client'
import { Card } from '../../components/Card'

type Tab = 'throw' | 'pick' | 'mine'

/** 匿名信（原漂流瓶，how.md 2.4）：浅色，写信卡片用信纸样式。 */
export default function BottlesPage() {
  const nav = useNavigate()
  const ws = getWsClient()
  const setToast = useUiStore((s) => s.setToast)
  const [tab, setTab] = useState<Tab>('throw')
  const [content, setContent] = useState('')
  const [picked, setPicked] = useState<PickedBottle | null>(null)
  const [emptySea, setEmptySea] = useState(false)
  const [replyText, setReplyText] = useState('')
  const [mine, setMine] = useState<MyBottle[]>([])
  const [unreadBadge, setUnreadBadge] = useState(0)

  useEffect(() => {
    const off = ws.on('bottle.reply', () => {
      setToast('你收到一条瓶子回复')
      setUnreadBadge((n) => n + 1)
    })
    return off
  }, [ws, setToast])

  useEffect(() => {
    if (tab !== 'mine') return
    myBottles()
      .then(setMine)
      .catch((e) => setToast(errorMessage((e as { code?: string }).code)))
  }, [tab, setToast])

  async function doThrow() {
    try {
      await postBottle(content)
      setContent('')
      setToast('瓶子已投入海里')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  async function doPick() {
    setEmptySea(false)
    try {
      setPicked(await pickBottle())
    } catch (e) {
      const code = (e as { code?: string }).code
      if (code === 'empty_sea') setEmptySea(true)
      else setToast(errorMessage(code))
      setPicked(null)
    }
  }

  async function doThrowBack() {
    if (!picked) return
    try {
      await throwBackBottle(picked.id)
      setPicked(null)
      setToast('已扔回海里')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  async function doReply() {
    if (!picked) return
    try {
      const r = await replyBottle(picked.id, replyText)
      setPicked(null)
      setReplyText('')
      nav(`/bottles/${r.conversationId}`)
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  async function doReport() {
    if (!picked) return
    try {
      await reportBottle(picked.id, { reason: 'other', note: '' })
      setPicked(null)
      setToast('举报已提交')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  const throwLen = [...content].length

  const tabClass = (k: Tab) =>
    `rounded-sm px-3 py-1.5 text-sm transition duration-150 ease-out ${
      tab === k ? 'bg-brand text-white' : 'text-lobby-sub hover:text-lobby-ink'
    }`

  return (
    <main className="mx-auto max-w-3xl px-4 py-8">
      <h1 className="text-2xl font-bold tracking-tight text-lobby-ink">匿名信</h1>
      <p className="mb-6 text-sm text-lobby-sub">漂流瓶：写一封没有署名的信，等有缘人捞起。</p>

      <div className="mb-5 flex gap-2">
        <button type="button" className={tabClass('throw')} onClick={() => setTab('throw')}>
          写信
        </button>
        <button type="button" className={tabClass('pick')} onClick={() => setTab('pick')}>
          捞一封
        </button>
        <button type="button" className={tabClass('mine')} onClick={() => setTab('mine')}>
          {unreadBadge > 0 ? `我的信 (${unreadBadge})` : '我的信'}
        </button>
      </div>

      {tab === 'throw' && (
        <Card className="p-5">
          <div className="flex gap-4">
            <span aria-hidden className="w-px shrink-0 rounded-full bg-brand/40" />
            <div className="flex-1 space-y-3">
              <textarea
                aria-label="瓶子内容"
                className="w-full resize-none rounded-sm border border-lobby-line bg-lobby-bg p-3 text-sm text-lobby-ink outline-none"
                rows={6}
                maxLength={500}
                value={content}
                onChange={(e) => setContent(e.target.value)}
                placeholder="写点什么，投进海里…"
              />
              <div className="flex items-center justify-between">
                <span className="text-xs text-lobby-sub">{throwLen} / 500</span>
                <button
                  type="button"
                  className="rounded-sm bg-brand px-4 py-2 text-sm text-white transition duration-150 ease-out disabled:opacity-40"
                  disabled={content.trim() === '' || throwLen > 500}
                  onClick={doThrow}
                >
                  投入海里
                </button>
              </div>
            </div>
          </div>
        </Card>
      )}

      {tab === 'pick' && (
        <div className="space-y-3">
          <button
            type="button"
            className="rounded-sm bg-brand px-4 py-2 text-sm text-white transition duration-150 ease-out"
            onClick={doPick}
          >
            捞一个瓶子
          </button>
          {emptySea && <p className="text-sm text-lobby-sub">海里暂时没有瓶子，晚点再来看看。</p>}
          {picked && (
            <Card className="space-y-3 p-5">
              <p className="whitespace-pre-wrap break-words text-sm text-lobby-ink">{picked.content}</p>
              <div className="flex gap-2 text-sm">
                <button
                  type="button"
                  className="rounded-sm bg-lobby-bg px-3 py-1 text-lobby-ink transition duration-150 ease-out"
                  onClick={doThrowBack}
                >
                  扔回海里
                </button>
                <button
                  type="button"
                  className="rounded-sm bg-lobby-bg px-3 py-1 text-lobby-ink transition duration-150 ease-out"
                  onClick={doReport}
                >
                  举报
                </button>
              </div>
              <div className="flex gap-2">
                <input
                  aria-label="回复内容"
                  className="flex-1 rounded-sm border border-lobby-line bg-lobby-bg px-3 py-1.5 text-sm text-lobby-ink"
                  value={replyText}
                  onChange={(e) => setReplyText(e.target.value)}
                  placeholder="回复这个瓶子…"
                />
                <button
                  type="button"
                  className="rounded-sm bg-brand px-4 py-1.5 text-sm text-white transition duration-150 ease-out disabled:opacity-40"
                  disabled={replyText.trim() === ''}
                  onClick={doReply}
                >
                  回复
                </button>
              </div>
            </Card>
          )}
        </div>
      )}

      {tab === 'mine' && (
        <div className="space-y-3">
          {mine.length === 0 && <p className="text-sm text-lobby-sub">你还没有投放瓶子。</p>}
          {mine.map((b) => (
            <Card key={b.id} className="p-5">
              <p className="whitespace-pre-wrap break-words text-sm text-lobby-ink">{b.contentPreview}</p>
              <p className="mt-1 text-xs text-lobby-sub">{new Date(b.createdAt).toLocaleString()}</p>
              <div className="mt-2 space-y-1">
                {b.replies.map((r) => (
                  <button
                    key={r.conversationId}
                    type="button"
                    className="block text-sm text-brand transition duration-150 ease-out"
                    onClick={() => nav(`/bottles/${r.conversationId}`)}
                  >
                    查看回复
                    {r.unread > 0 && (
                      <span className="ml-1 rounded-full bg-brand/15 px-1.5 text-xs text-brand">
                        {r.unread}
                      </span>
                    )}
                  </button>
                ))}
              </div>
            </Card>
          ))}
        </div>
      )}
    </main>
  )
}
