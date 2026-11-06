import { Link } from 'react-router-dom'
import { Avatar } from '../../components/Avatar'

const ENTRIES = [
  { to: '/match', title: '随机匹配', desc: '和陌生人一对一随机聊天，不合适可以换下一个' },
  { to: '/rooms', title: '聊天室', desc: '按话题进入房间，和大家一起聊' },
  { to: '/bottles', title: '漂流瓶', desc: '投一段文字，等有缘人捞起并回复' },
  { to: '/p/new', title: '私密链接', desc: '生成链接发给熟人，两人私聊，可阅后即焚' },
]

/** 首页：四个入口卡片。 */
export default function HomePage() {
  return (
    <main className="mx-auto max-w-3xl px-4 py-8">
      <h1 className="text-3xl font-bold tracking-tight mb-1">AnonChat</h1>
      <p className="text-slate-400 mb-8">匿名聊天，打开就能聊。</p>
      <div className="grid gap-4 sm:grid-cols-2">
        {ENTRIES.map((e) => (
          <Link
            key={e.to}
            to={e.to}
            className="block rounded-xl bg-slate-900 hover:bg-slate-800 transition p-5 border border-slate-800"
          >
            <h2 className="text-lg font-semibold mb-1">{e.title}</h2>
            <p className="text-sm text-slate-400">{e.desc}</p>
          </Link>
        ))}
      </div>
      <div className="mt-10 flex items-center gap-3 text-slate-500 text-sm">
        <Avatar seed="anonchat-footer" size={28} />
        聊天记录最多保留 7 天；请文明聊天，遇到骚扰可以屏蔽和举报。
      </div>
    </main>
  )
}
