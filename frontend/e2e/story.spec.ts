import { test, expect } from '@playwright/test'
import { loginAdmin, openHome, sendChat } from './helpers'

test('J 故事书完整流程：发起 → 认领 → 各发 5 条 → 结束 → 双方同意 → 审核 → 书架可见', async ({ browser }) => {
  const host = await openHome(browser)
  const guest = await openHome(browser)
  const title = `雨夜书店-${Date.now()}`

  // 发起。
  await host.goto('/stories/new')
  await host.getByLabel('故事标题').fill(title)
  await host.getByLabel('故事开场').fill('雨下得很大，书店里只剩下一个客人，他站在门口抖落伞上的水。')
  await host.getByLabel('角色一').fill('店主')
  await host.getByLabel('角色二').fill('旅人')
  await host.getByRole('button', { name: '发布故事' }).click()
  await expect(host.getByText('故事已发布，等待认领')).toBeVisible({ timeout: 20000 })
  await expect(host.getByText(/正在等待另一个人认领「旅人」/)).toBeVisible({ timeout: 20000 })

  // 认领。
  await guest.goto('/stories')
  await expect(guest.getByText(title)).toBeVisible({ timeout: 20000 })
  await guest.getByRole('button', { name: '认领「旅人」' }).first().click()
  await expect(guest.getByLabel('消息输入框')).toBeVisible({ timeout: 20000 })

  // 发起人这边自动进入剧场。
  await expect(host.getByLabel('消息输入框')).toBeVisible({ timeout: 25000 })

  // 双方各发 5 条（有效场次要求）。
  for (let i = 1; i <= 5; i++) {
    await sendChat(host, `店主第${i}句`)
    await expect(guest.getByText(`店主第${i}句`)).toBeVisible({ timeout: 20000 })
    await sendChat(guest, `旅人第${i}句`)
    await expect(host.getByText(`旅人第${i}句`)).toBeVisible({ timeout: 20000 })
  }

  // 剧场里显示角色名而不是昵称。
  await expect(host.locator('header').getByText(/店主 · 旅人/)).toBeVisible()

  // 等待有效时长（E2E 下 STORY_MIN_DURATION=1s）后结束。
  await host.waitForTimeout(1500)
  await host.getByRole('button', { name: '结束演出' }).click()
  await expect(host.getByText('演出结束，判定为有效场次')).toBeVisible({ timeout: 20000 })

  // 双方同意。
  await host.getByRole('button', { name: '申请收录' }).click()
  await expect(host.getByText('已同意收录')).toBeVisible({ timeout: 20000 })
  await guest.getByRole('button', { name: '同意收录' }).click()
  await expect(guest.getByText('已同意收录')).toBeVisible({ timeout: 20000 })

  // 管理员审核通过。
  const admin = await loginAdmin(browser)
  await admin.getByRole('link', { name: '作品审核' }).click()
  const workRow = admin.getByTestId('admin-work').filter({ hasText: title }).first()
  await expect(workRow).toBeVisible({ timeout: 20000 })
  await workRow.getByRole('button', { name: '通过' }).click()
  await expect(admin.getByText('作品已通过')).toBeVisible({ timeout: 20000 })

  // 书架可见，并能阅读。
  await guest.goto('/stories')
  await guest.getByRole('button', { name: '书架' }).click()
  await expect(guest.getByText(`《${title}》`)).toBeVisible({ timeout: 20000 })
  await guest.getByText(`《${title}》`).click()
  await expect(guest.getByText('店主第1句')).toBeVisible({ timeout: 20000 })

  await host.context().close()
  await guest.context().close()
  await admin.context().close()
})
