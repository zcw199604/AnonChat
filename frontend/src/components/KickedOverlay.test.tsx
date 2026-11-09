import { afterEach, expect, it } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { KickedOverlay } from './KickedOverlay'
import { useUiStore } from '../stores/ui'

afterEach(() => { cleanup(); useUiStore.setState({ kicked: null }) })

it.each([
  ['duplicate_session', '当前身份已在其他环境登录，请先在其他环境退出后重试'],
  ['ip_conn_limit', '当前网络下的连接数已达上限，请关闭其他页面或设备后重试'],
])('展示 %s 的连接拒绝原因', (reason, text) => {
  useUiStore.getState().setKicked(reason)
  render(<KickedOverlay />)
  expect(screen.getByText(text)).toBeInTheDocument()
})
