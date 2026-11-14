import { describe, it, expect, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { ProfileCard } from './ProfileCard'
import { useIdentityStore } from '../../stores/identity'
import type { MeInfo } from '../../lib/api/client'

/**
 * 首页资料卡（how.md 2.2，按产品决定调整）：
 * 头像行显示自己的随机中文昵称，不再把打码的匿名编号当作名字展示；
 * 匿名编号仅作为联系客服的辅助信息保留在昵称下方。
 */

const me: MeInfo = {
  nickname: '迷路的水獭',
  avatarSeed: 'seed-self',
  verified: false,
  verificationMode: 'off',
  publicCode: 'ABCD3FGHJK',
  profile: { gender: 'secret', age: null, province: null, provincePublic: false, tags: [], cleanMode: false },
  matchPrefs: { gender: 'any', sameProvince: false },
  vip: { active: false, until: null },
  broadcastCards: 0,
}

beforeEach(() => {
  localStorage.clear()
  useIdentityStore.setState({
    me,
    nickname: me.nickname,
    avatarSeed: me.avatarSeed,
    publicCode: me.publicCode,
  })
})

describe('ProfileCard 头像行', () => {
  it('显示自己的昵称，而不是打码的匿名编号', () => {
    render(<ProfileCard onMatch={() => undefined} nickname="迷路的水獭" />)

    expect(screen.getByTestId('profile-nickname')).toHaveTextContent('迷路的水獭')
    // 昵称是头像行里最醒目的文本，打码编号只作为小号灰字保留。
    expect(screen.queryByText('AB****GHJK')).not.toBeNull()
    expect(screen.getByLabelText('复制匿名编号')).toBeInTheDocument()
  })

  it('父组件未传昵称时回退 store 里的昵称', () => {
    render(<ProfileCard onMatch={() => undefined} nickname="" />)
    expect(screen.getByTestId('profile-nickname')).toHaveTextContent('迷路的水獭')
  })
})
