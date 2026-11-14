import { test, expect } from '@playwright/test'
import { openHome, startMatch, sendChat } from './helpers'

test('A 随机匹配：互相收发消息，二次确认换一个→对方被跳过', async ({ browser }) => {
  const a = await openHome(browser)
  const b = await openHome(browser)

  await startMatch(a)
  await startMatch(b)

  await expect(a.getByLabel('消息输入框')).toBeVisible({ timeout: 20000 })
  await expect(b.getByLabel('消息输入框')).toBeVisible({ timeout: 20000 })

  await sendChat(a, 'hello-from-a')
  await expect(b.getByText('hello-from-a')).toBeVisible({ timeout: 20000 })

  await sendChat(b, 'hi-from-b')
  await expect(a.getByText('hi-from-b')).toBeVisible({ timeout: 20000 })

  await a.getByRole('button', { name: '换一个', exact: true }).click()
  await a.getByRole('button', { name: '确认换人？', exact: true }).click()
  await expect(b.getByText('对方跳过了你')).toBeVisible({ timeout: 20000 })

  await a.context().close()
  await b.context().close()
})
