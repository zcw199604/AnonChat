import { test, expect } from '@playwright/test'
import { openHome, sendChat } from './helpers'

test('D 私密链接：密码、阅后即焚、满员、销毁', async ({ browser }) => {
  const a = await openHome(browser)
  const b = await openHome(browser)
  const c = await openHome(browser)

  await a.goto('/p/new')
  await a.getByLabel('访问密码').fill('pass1234')
  await a.getByLabel('开启阅后即焚').check()
  await a.getByRole('button', { name: '创建链接' }).click()

  const link = await a.getByLabel('私密链接').inputValue()
  const token = link.split('/p/')[1]
  await a.getByRole('button', { name: '进入聊天' }).click()
  await expect(a.getByLabel('消息输入框')).toBeVisible({ timeout: 20000 })

  // 乙先输错密码
  await b.goto(`/p/${token}`)
  await expect(b.getByLabel('访问密码')).toBeVisible({ timeout: 20000 })
  await b.getByLabel('访问密码').fill('wrongpass')
  await b.getByRole('button', { name: '加入' }).click()
  await expect(b.getByText('密码错误')).toBeVisible({ timeout: 20000 })
  await b.getByLabel('访问密码').fill('pass1234')
  await b.getByRole('button', { name: '加入' }).click()
  await expect(b.getByLabel('消息输入框')).toBeVisible({ timeout: 20000 })

  // 阅后即焚：乙看到后 10 秒内从双方界面消失
  const burnMsg = `burn-${Date.now()}`
  await sendChat(a, burnMsg)
  await expect(b.getByText(burnMsg)).toBeVisible({ timeout: 20000 })
  await expect(b.getByText(burnMsg)).toBeHidden({ timeout: 25000 })
  await expect(a.getByText(burnMsg)).toBeHidden({ timeout: 25000 })

  // 第三人打开：链接已满
  await c.goto(`/p/${token}`)
  await expect(c.getByText('该链接已满')).toBeVisible({ timeout: 20000 })

  // 甲销毁，乙看到会话已被销毁
  await a.getByRole('button', { name: '销毁会话' }).click()
  await a.getByRole('button', { name: '确认销毁' }).click()
  await expect(b.getByText('会话已被销毁')).toBeVisible({ timeout: 20000 })

  await a.context().close()
  await b.context().close()
  await c.context().close()
})
