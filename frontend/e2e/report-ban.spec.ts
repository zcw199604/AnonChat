import { test, expect } from '@playwright/test'
import { openHome, startMatch, sendChat } from './helpers'
import { E2E_ADMIN_USER, E2E_ADMIN_PASS } from './global-setup'

test('E 举报与封禁：举报消息、管理员封禁、被踢出且刷新后仍被封', async ({ browser }) => {
  const a = await openHome(browser)
  const b = await openHome(browser)

  await startMatch(a)
  await startMatch(b)
  await expect(b.getByLabel('消息输入框')).toBeVisible({ timeout: 20000 })

  const abuse = `abuse-${Date.now()}`
  await sendChat(a, abuse)
  await expect(b.getByText(abuse)).toBeVisible({ timeout: 20000 })

  // 乙举报甲的消息
  await b.getByRole('button', { name: '更多操作' }).click()
  await b.getByRole('button', { name: '举报' }).click()
  const dialog = b.getByRole('dialog', { name: '举报' })
  await dialog.getByRole('checkbox').first().check()
  await dialog.getByLabel('举报原因').selectOption('harassment')
  await b.getByRole('button', { name: '提交举报' }).click()
  await expect(b.getByText('举报已提交')).toBeVisible({ timeout: 20000 })

  // 管理员登录并处理举报（封禁身份）
  const admin = await openHome(browser)
  await admin.goto('/admin')
  await admin.getByLabel('用户名').fill(E2E_ADMIN_USER)
  await admin.getByLabel('密码').fill(E2E_ADMIN_PASS)
  await admin.getByRole('button', { name: '登录' }).click()
  await admin.getByRole('link', { name: /举报处理/ }).click()
  await admin.getByText('harassment').first().click()
  await expect(admin.getByText('证据快照')).toBeVisible({ timeout: 20000 })
  await admin.getByLabel('封禁原因').fill('e2e-ban')
  await admin.getByRole('button', { name: '封禁并处理' }).click()
  await expect(admin.getByText('已封禁并处理举报')).toBeVisible({ timeout: 20000 })

  // 甲被踢出：全屏提示；刷新后仍被封
  await expect(a.getByText('你已被封禁')).toBeVisible({ timeout: 20000 })
  await a.reload()
  await expect(a.getByText('你已被封禁')).toBeVisible({ timeout: 30000 })

  await a.context().close()
  await b.context().close()
  await admin.context().close()
})
