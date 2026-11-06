import { useCallback, useEffect, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { Card } from '../../components/Card'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { deleteWork, getWork, likeWork, reportWork, unlikeWork, withdrawWork } from '../../lib/api/client'
import type { WorkDetail } from '../../lib/api/client'

/** 作品阅读页（how.md 2.4 浅色）：对话体排版、点赞、举报、删除、撤回。 */
export default function WorkPage() {
  const { workId } = useParams()
  const nav = useNavigate()
  const setToast = useUiStore((s) => s.setToast)
  const [work, setWork] = useState<WorkDetail | null>(null)

  const load = useCallback(async () => {
    if (!workId) return
    try {
      setWork(await getWork(workId))
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }, [workId, setToast])

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- 首次挂载时拉取远端数据，状态更新在请求返回之后
    void load()
  }, [load])

  async function toggleLike() {
    if (!work || !workId) return
    try {
      const r = work.liked ? await unlikeWork(workId) : await likeWork(workId)
      setWork({ ...work, liked: !work.liked, likeCount: r.likeCount })
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  async function doReport() {
    if (!workId) return
    try {
      await reportWork(workId, { reason: 'other', note: '' })
      setToast('举报已提交')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  async function doDelete() {
    if (!workId) return
    try {
      await deleteWork(workId)
      setToast('作品已删除')
      nav('/stories')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  async function doWithdraw() {
    if (!workId) return
    try {
      await withdrawWork(workId)
      setToast('已撤回同意，作品已下架')
      nav('/stories')
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    }
  }

  if (!work) {
    return <main className="mx-auto max-w-2xl px-4 py-10 text-sm text-lobby-sub">加载中…</main>
  }

  return (
    <main className="mx-auto max-w-2xl px-4 py-8">
      <h1 className="text-2xl font-bold tracking-tight text-lobby-ink">《{work.title}》</h1>
      <p className="mt-1 text-xs text-lobby-sub">
        {work.roleA} · {work.roleB}
        {work.status !== 'approved' ? ` · ${work.status === 'pending' ? '待审核' : '仅参与者可见'}` : ''}
      </p>

      <Card className="mt-5 space-y-4 p-6">
        <p className="whitespace-pre-wrap text-sm leading-relaxed text-lobby-sub">{work.opening}</p>
        <div className="space-y-3">
          {work.lines.map((line, i) => {
            const self = line.role === work.roleA
            return (
              <div key={i} className={`flex flex-col gap-1 ${self ? 'items-start' : 'items-end'}`}>
                <span className="text-[11px] text-lobby-sub">{line.role}</span>
                <p
                  className={`max-w-[80%] whitespace-pre-wrap break-words rounded-card px-3.5 py-2 text-sm ${
                    self ? 'bg-lobby-bg text-lobby-ink' : 'bg-brand/10 text-lobby-ink'
                  }`}
                >
                  {line.text}
                </p>
              </div>
            )
          })}
        </div>
      </Card>

      <div className="mt-4 flex flex-wrap items-center gap-2 text-sm">
        <button
          type="button"
          onClick={toggleLike}
          className="rounded-full border border-lobby-line bg-lobby-card px-4 py-1.5 text-lobby-ink shadow-soft transition duration-150 ease-out"
        >
          {work.liked ? '♥' : '♡'} {work.likeCount}
        </button>
        <button
          type="button"
          onClick={doReport}
          className="rounded-full border border-lobby-line bg-lobby-card px-4 py-1.5 text-lobby-ink shadow-soft transition duration-150 ease-out"
        >
          举报
        </button>
        {work.mine === 'host' && (
          <button
            type="button"
            onClick={doDelete}
            className="rounded-full bg-danger px-4 py-1.5 text-white transition duration-150 ease-out"
          >
            删除作品
          </button>
        )}
        {work.mine === 'guest' && (
          <button
            type="button"
            onClick={doWithdraw}
            className="rounded-full border border-lobby-line bg-lobby-card px-4 py-1.5 text-lobby-ink shadow-soft transition duration-150 ease-out"
          >
            撤回同意
          </button>
        )}
        <button
          type="button"
          onClick={() => nav('/stories')}
          className="text-xs text-lobby-sub underline-offset-2 hover:underline"
        >
          返回书架
        </button>
      </div>
    </main>
  )
}
