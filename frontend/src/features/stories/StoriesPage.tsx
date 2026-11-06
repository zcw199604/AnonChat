import { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { Card } from '../../components/Card'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { joinStory, listWaitingStories, listWorks } from '../../lib/api/client'
import type { WaitingStory, WorkListItem } from '../../lib/api/client'

type Tab = 'waiting' | 'shelf'

/** 故事书（how.md 2.4）：等待开演 / 书架两个 Tab，以及发起故事入口。 */
export default function StoriesPage() {
  const nav = useNavigate()
  const setToast = useUiStore((s) => s.setToast)
  const [tab, setTab] = useState<Tab>('waiting')
  const [waiting, setWaiting] = useState<WaitingStory[]>([])
  const [works, setWorks] = useState<WorkListItem[]>([])

  const load = useCallback(() => {
    listWaitingStories()
      .then(setWaiting)
      .catch((e) => setToast(errorMessage((e as { code?: string }).code)))
    listWorks()
      .then(setWorks)
      .catch(() => undefined)
  }, [setToast])

  useEffect(() => {
    load()
  }, [load])

  async function doJoin(id: string) {
    try {
      const r = await joinStory(id)
      nav(`/stories/${id}/play?conversationId=${r.conversationId}`)
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
      load()
    }
  }

  const tabClass = (k: Tab) =>
    `rounded-sm px-3 py-1.5 text-sm transition duration-150 ease-out ${
      tab === k ? 'bg-brand text-white' : 'text-lobby-sub hover:text-lobby-ink'
    }`

  return (
    <main className="mx-auto max-w-3xl px-4 py-8">
      <div className="mb-5 flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight text-lobby-ink">故事书 · 公测</h1>
          <p className="text-sm text-lobby-sub">两个人演一个故事，演完可以收录成作品。</p>
        </div>
        <Link
          to="/stories/new"
          className="rounded-sm bg-brand px-4 py-2 text-sm text-white transition duration-150 ease-out"
        >
          发起故事
        </Link>
      </div>

      <div className="mb-4 flex gap-2">
        <button type="button" className={tabClass('waiting')} onClick={() => setTab('waiting')}>
          等待开演
        </button>
        <button type="button" className={tabClass('shelf')} onClick={() => setTab('shelf')}>
          书架
        </button>
      </div>

      {tab === 'waiting' && (
        <div className="space-y-3">
          {waiting.length === 0 && <p className="text-sm text-lobby-sub">暂时没有等待开演的故事，去发起一个吧。</p>}
          {waiting.map((s) => (
            <Card key={s.id} className="space-y-2 p-5">
              <div className="flex items-start justify-between gap-3">
                <div>
                  <h2 className="text-sm font-semibold text-lobby-ink">{s.title}</h2>
                  <p className="mt-1 line-clamp-2 text-xs text-lobby-sub">{s.opening}</p>
                </div>
                <button
                  type="button"
                  onClick={() => void doJoin(s.id)}
                  className="shrink-0 rounded-sm bg-brand/15 px-3 py-1 text-xs text-brand transition duration-150 ease-out"
                >
                  认领「{s.openRole}」
                </button>
              </div>
              <div className="flex flex-wrap gap-2 text-[11px] text-lobby-sub">
                <span>角色：{s.roleA} / {s.roleB}</span>
                {s.tags.map((t) => (
                  <span key={t} className="rounded-full bg-lobby-bg px-2 py-0.5">
                    #{t}
                  </span>
                ))}
              </div>
            </Card>
          ))}
        </div>
      )}

      {tab === 'shelf' && (
        <div className="space-y-3">
          {works.length === 0 && <p className="text-sm text-lobby-sub">书架还空着，等待第一个被收录的作品。</p>}
          {works.map((w) => (
            <Link key={w.id} to={`/works/${w.id}`} className="block">
              <Card className="space-y-1 p-5 transition duration-150 ease-out hover:-translate-y-0.5">
                <h2 className="text-sm font-semibold text-lobby-ink">《{w.title}》</h2>
                <p className="line-clamp-2 text-xs text-lobby-sub">{w.opening}</p>
                <p className="text-[11px] text-lobby-sub">
                  {w.roleA} / {w.roleB} · {w.likeCount} 赞
                </p>
              </Card>
            </Link>
          ))}
        </div>
      )}
    </main>
  )
}
