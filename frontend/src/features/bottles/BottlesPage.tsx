import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { getWsClient } from '../../lib/ws'
import { errorMessage } from '../../lib/api/errorMessages'
import { useUiStore } from '../../stores/ui'
import { postBottle, pickBottle, throwBackBottle, replyBottle, myBottles, reportBottle } from '../../lib/api/client'
import type { PickedBottle, MyBottle } from '../../lib/api/client'

type Tab = 'throw' | 'pick' | 'mine'

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

  return (
    <main className="mx-auto max-w-3xl px-4 py-6">
      <h1 className="text-2xl font-bold mb-4">漂流瓶</h1>
      <div className="flex gap-2 mb-4 text-sm">
        {([
          ['throw', '扔瓶子'],
          ['pick', '捞瓶子'],
          ['mine', unreadBadge > 0 ? `我的瓶子 (${unreadBadge})` : '我的瓶子'],
        ] as Array<[Tab, string]>).map(([k, label]) => (
          <button
            key={k}
            type="button"
            className={`px-3 py-1 rounded ${tab === k ? 'bg-indigo-600 text-white' : 'bg-slate-800'}`}
            onClick={() => setTab(k)}
          >
            {label}
          </button>
        ))}
      </div>

      {tab === 'throw' && (
        <div className="space-y-2">
          <textarea
            aria-label="瓶子内容"
            className="w-full bg-slate-800 rounded p-3"
            rows={6}
            maxLength={500}
            value={content}
            onChange={(e) => setContent(e.target.value)}
            placeholder="写点什么，投进海里…"
          />
          <div className="text-xs text-slate-500">{throwLen} / 500</div>
          <button type="button" className="px-4 py-2 rounded bg-indigo-600 text-white disabled:opacity-40" disabled={content.trim() === '' || throwLen > 500} onClick={doThrow}>
            投入海里
          </button>
        </div>
      )}

      {tab === 'pick' && (
        <div className="space-y-3">
          <button type="button" className="px-4 py-2 rounded bg-indigo-600 text-white" onClick={doPick}>
            捞一个瓶子
          </button>
          {emptySea && <p className="text-slate-400">海里暂时没有瓶子，晚点再来看看。</p>}
          {picked && (
            <div className="rounded-lg bg-slate-900 border border-slate-800 p-4 space-y-3">
              <p className="whitespace-pre-wrap break-words text-slate-100">{picked.content}</p>
              <div className="flex gap-2 text-sm">
                <button type="button" className="px-2 py-1 rounded bg-slate-800" onClick={doThrowBack}>
                  扔回海里
                </button>
                <button type="button" className="px-2 py-1 rounded bg-slate-800" onClick={doReport}>
                  举报
                </button>
              </div>
              <div className="flex gap-2">
                <input
                  aria-label="回复内容"
                  className="flex-1 bg-slate-800 rounded px-2 py-1"
                  value={replyText}
                  onChange={(e) => setReplyText(e.target.value)}
                  placeholder="回复这个瓶子…"
                />
                <button type="button" className="px-3 py-1 rounded bg-indigo-600 text-white disabled:opacity-40" disabled={replyText.trim() === ''} onClick={doReply}>
                  回复
                </button>
              </div>
            </div>
          )}
        </div>
      )}

      {tab === 'mine' && (
        <div className="space-y-2">
          {mine.length === 0 && <p className="text-slate-400">你还没有投放瓶子。</p>}
          {mine.map((b) => (
            <div key={b.id} className="rounded-lg bg-slate-900 border border-slate-800 p-4">
              <p className="text-slate-100 whitespace-pre-wrap break-words">{b.contentPreview}</p>
              <p className="text-xs text-slate-500 mt-1">{new Date(b.createdAt).toLocaleString()}</p>
              <div className="mt-2 space-y-1">
                {b.replies.map((r) => (
                  <button key={r.conversationId} type="button" className="block text-sm text-indigo-400" onClick={() => nav(`/bottles/${r.conversationId}`)}>
                    查看回复 {r.unread > 0 && <span className="ml-1 text-xs bg-red-600 text-white rounded px-1">{r.unread}</span>}
                  </button>
                ))}
              </div>
            </div>
          ))}
        </div>
      )}
    </main>
  )
}
