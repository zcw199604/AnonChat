import { test, expect, type Page } from '@playwright/test'
import { openHome, sendChat } from './helpers'

async function pickUntil(page: Page, content: string) {
  for (let i = 0; i < 5; i++) {
    await page.getByRole('button', { name: '捞一个瓶子' }).click()
    await expect
      .poll(
        async () => {
          if (await page.getByText(content).isVisible().catch(() => false)) return 'found'
          if (await page.getByLabel('回复内容').isVisible().catch(() => false)) return 'other'
          if (await page.getByText('海里暂时没有瓶子，晚点再来看看。').isVisible().catch(() => false)) return 'empty'
          return 'wait'
        },
        { timeout: 10000 },
      )
      .not.toBe('wait')
    if (await page.getByText(content).isVisible().catch(() => false)) return
    const tb = page.getByRole('button', { name: '扔回海里' })
    if (await tb.isVisible().catch(() => false)) await tb.click()
  }
  throw new Error('未能在 5 次内捞到目标瓶子')
}

test('C 漂流瓶：投放、捞取、回复、我的瓶子未读', async ({ browser }) => {
  const a = await openHome(browser)
  const b = await openHome(browser)
  const content = `bottle-${Date.now()}-${Math.floor(Math.random() * 1e6)}`

  await a.goto('/bottles')
  await a.getByLabel('瓶子内容').fill(content)
  await a.getByRole('button', { name: '投入海里' }).click()
  await expect(a.getByText('瓶子已投入海里')).toBeVisible({ timeout: 20000 })

  await b.goto('/bottles')
  await b.getByRole('button', { name: '捞一封' }).click()
  await pickUntil(b, content)

  await b.getByLabel('回复内容').fill('reply-from-b')
  await b.getByRole('button', { name: '回复' }).click()
  await expect(b.getByLabel('消息输入框')).toBeVisible({ timeout: 20000 })
  await sendChat(b, 'bottle-reply-text')
  await expect(b.getByText('bottle-reply-text')).toBeVisible({ timeout: 20000 })

  await a.getByRole('button', { name: /我的信/ }).click()
  await expect(a.getByText(content)).toBeVisible({ timeout: 20000 })
  await a.getByRole('button', { name: /查看回复/ }).first().click()
  await expect(a.getByLabel('消息输入框')).toBeVisible({ timeout: 20000 })
  await expect(a.getByText('bottle-reply-text')).toBeVisible({ timeout: 20000 })

  await a.context().close()
  await b.context().close()
})
