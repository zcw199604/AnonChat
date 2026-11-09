import { test, expect } from '@playwright/test'
import { openHome, redeemVip } from './helpers'

test('F 资料保存后刷新仍然保留', async ({ browser }) => {
  const page = await openHome(browser)

  // 设置性别、年龄、省份与清流模式。
  await page.getByRole('group', { name: '性别', exact: true }).getByRole('button', { name: '女' }).click()
  await page.getByLabel('年龄').fill('28')
  await page.getByLabel('省份', { exact: true }).selectOption('浙江')
  await page.getByLabel('清流模式').click()

  // 防抖 500ms 后保存，等待请求完成再刷新。
  await page.waitForTimeout(1200)
  await page.reload()
  await expect(page.getByTestId('ws-connected')).toBeVisible({ timeout: 30000 })

  await expect(
    page.getByRole('group', { name: '性别', exact: true }).getByRole('button', { name: '女' }),
  ).toHaveAttribute('aria-pressed', 'true')
  await expect(page.getByLabel('年龄')).toHaveValue('28')
  await expect(page.getByLabel('省份', { exact: true })).toHaveValue('浙江')
  await expect(page.getByLabel('清流模式')).toHaveAttribute('aria-checked', 'true')

  await page.context().close()
})

test('G 兑换激活码后首页显示 VIP 徽标', async ({ browser }) => {
  const page = await openHome(browser)

  // 兑换前：首页已没有 VipLock（匹配偏好区块已移除），也不显示 VIP 徽标。
  await expect(page.getByTestId('vip-lock')).toHaveCount(0)
  await expect(page.getByText(/VIP 至 \d{4}-\d{2}-\d{2}/)).toHaveCount(0)

  await redeemVip(page)
  await expect(page.getByText('VIP 生效中')).toBeVisible({ timeout: 20000 })

  await page.goto('/')
  await expect(page.getByTestId('ws-connected')).toBeVisible({ timeout: 30000 })
  await expect(page.getByTestId('vip-lock')).toHaveCount(0)
  // VIP 徽标出现。
  await expect(page.getByText(/VIP 至 \d{4}-\d{2}-\d{2}/)).toBeVisible()

  await page.context().close()
})
