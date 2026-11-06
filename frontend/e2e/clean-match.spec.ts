import { test, expect } from '@playwright/test'
import { openHome, sendChat } from './helpers'

test('H 双清流配对：看到匿名代号与对方资料芯片', async ({ browser }) => {
  const a = await openHome(browser)
  const b = await openHome(browser)

  // 两边都开启清流模式，并各自填写公开资料。
  for (const [page, province, age] of [
    [a, '浙江', '28'],
    [b, '浙江', '31'],
  ] as const) {
    await page.getByLabel('清流模式').click()
    await page.getByLabel('年龄').fill(age)
    await page.getByLabel('省份', { exact: true }).selectOption(province)
    await page.getByRole('switch', { name: '公开省份' }).click()
    await page.waitForTimeout(900)
  }

  await a.getByTestId('match-button').click()
  await b.getByTestId('match-button').click()

  // 双方进入会话：显示匿名代号「匿名·XXXX」。
  await expect(a.getByText(/^匿名·[0-9A-F]{4}$/)).toBeVisible({ timeout: 20000 })
  await expect(b.getByText(/^匿名·[0-9A-F]{4}$/)).toBeVisible({ timeout: 20000 })

  // 对方资料芯片：年龄与省份（省份仅在对方公开时出现）。
  await expect(a.locator('header').getByText('31', { exact: true })).toBeVisible({ timeout: 10000 })
  await expect(a.locator('header').getByText('浙江', { exact: true })).toBeVisible()
  await expect(b.locator('header').getByText('28', { exact: true })).toBeVisible({ timeout: 10000 })
  await expect(b.locator('header').getByText('浙江', { exact: true })).toBeVisible()

  await sendChat(a, 'clean-hello')
  await expect(b.getByText('clean-hello')).toBeVisible({ timeout: 20000 })

  await a.context().close()
  await b.context().close()
})
