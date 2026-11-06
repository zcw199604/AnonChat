import { test, expect } from '@playwright/test'
import { openHome, sendChat } from './helpers'

test('B 聊天室：在线人数、互发消息、离开后人数减少', async ({ browser }) => {
  const a = await openHome(browser)
  const b = await openHome(browser)

  await a.goto('/rooms')
  await b.goto('/rooms')
  await a.getByText('闲聊大厅').first().click()
  await b.getByText('闲聊大厅').first().click()

  await expect(a.getByText('房间在线 2 人')).toBeVisible({ timeout: 20000 })
  await expect(b.getByText('房间在线 2 人')).toBeVisible({ timeout: 20000 })

  const roomMsg = `room-${Date.now()}-${Math.floor(Math.random() * 1e6)}`
  await sendChat(a, roomMsg)
  await expect(b.getByText(roomMsg)).toBeVisible({ timeout: 20000 })

  await a.getByRole('button', { name: '返回列表' }).click()
  await expect(b.getByText('房间在线 1 人')).toBeVisible({ timeout: 20000 })

  await a.context().close()
  await b.context().close()
})
