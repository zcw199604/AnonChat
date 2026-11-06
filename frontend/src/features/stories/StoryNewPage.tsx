import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { Card } from '../../components/Card'
import { Segmented } from '../../components/Segmented'
import { TagPicker } from '../../components/TagPicker'
import { useUiStore } from '../../stores/ui'
import { errorMessage } from '../../lib/api/errorMessages'
import { createStory } from '../../lib/api/client'

/** 发起故事（how.md 2.4 浅色）。 */
export default function StoryNewPage() {
  const nav = useNavigate()
  const setToast = useUiStore((s) => s.setToast)
  const [title, setTitle] = useState('')
  const [opening, setOpening] = useState('')
  const [tags, setTags] = useState<string[]>([])
  const [roleA, setRoleA] = useState('')
  const [roleB, setRoleB] = useState('')
  const [hostRole, setHostRole] = useState<'a' | 'b'>('a')
  const [busy, setBusy] = useState(false)

  const canSubmit =
    [...title].length >= 2 &&
    [...title].length <= 30 &&
    [...opening].length >= 10 &&
    [...opening].length <= 500 &&
    roleA.trim() !== '' &&
    roleB.trim() !== '' &&
    roleA.trim() !== roleB.trim()

  async function submit() {
    if (!canSubmit) return
    setBusy(true)
    try {
      const story = await createStory({
        title: title.trim(),
        opening: opening.trim(),
        tags,
        roleA: roleA.trim(),
        roleB: roleB.trim(),
        hostRole,
      })
      setToast('故事已发布，等待认领')
      nav(`/stories/${story.id}/play`)
    } catch (e) {
      setToast(errorMessage((e as { code?: string }).code))
    } finally {
      setBusy(false)
    }
  }

  return (
    <main className="mx-auto max-w-2xl px-4 py-8">
      <h1 className="mb-6 text-2xl font-bold tracking-tight text-lobby-ink">发起故事</h1>
      <Card className="space-y-5 p-6">
        <div>
          <label className="mb-1 block text-sm text-lobby-sub">标题（2–30 字）</label>
          <input
            aria-label="故事标题"
            className="w-full rounded-sm border border-lobby-line bg-lobby-bg px-3 py-2 text-sm text-lobby-ink"
            maxLength={30}
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            placeholder="例如：雨夜书店"
          />
        </div>
        <div>
          <label className="mb-1 block text-sm text-lobby-sub">开场（10–500 字）</label>
          <textarea
            aria-label="故事开场"
            className="w-full resize-none rounded-sm border border-lobby-line bg-lobby-bg p-3 text-sm text-lobby-ink"
            rows={5}
            maxLength={500}
            value={opening}
            onChange={(e) => setOpening(e.target.value)}
            placeholder="写下故事的开头，给对方一个入口…"
          />
        </div>
        <div className="grid grid-cols-2 gap-3">
          <div>
            <label className="mb-1 block text-sm text-lobby-sub">角色一</label>
            <input
              aria-label="角色一"
              className="w-full rounded-sm border border-lobby-line bg-lobby-bg px-3 py-2 text-sm text-lobby-ink"
              maxLength={12}
              value={roleA}
              onChange={(e) => setRoleA(e.target.value)}
              placeholder="店主"
            />
          </div>
          <div>
            <label className="mb-1 block text-sm text-lobby-sub">角色二</label>
            <input
              aria-label="角色二"
              className="w-full rounded-sm border border-lobby-line bg-lobby-bg px-3 py-2 text-sm text-lobby-ink"
              maxLength={12}
              value={roleB}
              onChange={(e) => setRoleB(e.target.value)}
              placeholder="旅人"
            />
          </div>
        </div>
        <div className="flex items-center gap-3">
          <span className="text-sm text-lobby-sub">我扮演</span>
          <Segmented<'a' | 'b'>
            ariaLabel="我扮演的角色"
            value={hostRole}
            options={[
              { value: 'a', label: roleA || '角色一' },
              { value: 'b', label: roleB || '角色二' },
            ]}
            onChange={setHostRole}
          />
        </div>
        <div>
          <label className="mb-1 block text-sm text-lobby-sub">标签（最多 5 个）</label>
          <TagPicker selected={tags} onChange={setTags} />
        </div>
        <button
          type="button"
          className="rounded-sm bg-brand px-5 py-2 text-sm text-white transition duration-150 ease-out disabled:opacity-40"
          disabled={!canSubmit || busy}
          onClick={submit}
        >
          发布故事
        </button>
      </Card>
    </main>
  )
}
