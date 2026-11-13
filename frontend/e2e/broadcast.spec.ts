import { test, expect } from '@playwright/test'
import { loginAdmin, openHome, redeemVip } from './helpers'

test('I 广播：提交 → 管理员审核通过 → 首页横幅可见', async ({ browser }) => {
  const user = await openHome(browser)

  // 兑换激活码拿到广播卡。
  await redeemVip(user)

  // 提交广播。
  const content = `广播-${Date.now()}`
  await user.getByLabel('广播内容').fill(content)
  await user.getByRole('button', { name: '提交审核' }).click()
  await expect(user.getByText('广播已提交，等待审核')).toBeVisible({ timeout: 20000 })
  await expect(user.locator('li', { hasText: content })).toContainText('待审核', { timeout: 20000 })

  // 管理员通过审核。
  const admin = await loginAdmin(browser)
  await admin.getByRole('link', { name: '广播审核' }).click()
  const row = admin.getByTestId('admin-broadcast').filter({ hasText: content }).first()
  await expect(row).toBeVisible({ timeout: 20000 })
  await row.getByRole('button', { name: '通过' }).click()
  await expect(admin.getByText('已通过，展示 24 小时')).toBeVisible({ timeout: 20000 })

  // 回到首页：广播应以窄横幅形式展示通过审核的内容。
  await user.goto('/')
  await expect(user.getByTestId('ws-connected')).toBeVisible({ timeout: 30000 })
  await expect(user.getByTestId('broadcast-banner').getByText(content).first()).toBeVisible({
    timeout: 20000,
  })

  await user.context().close()
  await admin.context().close()
})
