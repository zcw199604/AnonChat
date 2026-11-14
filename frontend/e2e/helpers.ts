import { expect, type Browser, type Page } from '@playwright/test'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

/** 打开首页并等待匿名身份初始化与 WS 连接完成。 */
export async function openHome(browser: Browser): Promise<Page> {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await page.goto('/')
  await expect(page.getByTestId('ws-connected')).toBeVisible({ timeout: 30000 })
  return page
}

export async function startMatch(page: Page): Promise<void> {
  // 新版大厅：点击资料卡上的「匹配」大按钮，进入 /match?auto=1 后自动开始匹配。
  await page.getByTestId('match-button').click()
}

export async function sendChat(page: Page, text: string): Promise<void> {
  await expect(page.getByRole('button', { name: '进入聊天' })).toBeHidden({ timeout: 5000 })
  const input = page.getByLabel('消息输入框')
  await input.fill(text)
  await input.press('Enter')
}

/** 读取 e2eseed 写出的种子数据（含激活码明文）。 */
export function readSeed(): {
  adminUsername: string
  adminPassword: string
  activationCodes: string[]
  vipDays: number
  broadcastCards: number
} {
  const file = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '.seed.json')
  return JSON.parse(readFileSync(file, 'utf8'))
}

/**
 * 在 /vip 页面用种子里的激活码兑换 VIP。
 * 每个码只能用一次，因此按顺序尝试直到成功（支持 --repeat-each 重复运行）。
 */
export async function redeemVip(page: Page): Promise<void> {
  const seed = readSeed()
  await page.goto('/vip')
  for (const code of seed.activationCodes) {
    await page.getByLabel('激活码').fill(code)
    await page.getByRole('button', { name: '兑换' }).click()
    const ok = await page
      .getByText('兑换成功，VIP 特权已生效')
      .waitFor({ timeout: 8000 })
      .then(() => true)
      .catch(() => false)
    if (ok) return
  }
  throw new Error('种子里的激活码都已用完，无法兑换 VIP')
}

/** 管理员登录并返回页面（已登录状态）。 */
export async function loginAdmin(browser: Browser): Promise<Page> {
  const seed = readSeed()
  const page = await openHome(browser)
  await page.goto('/admin')
  await page.getByLabel('用户名').fill(seed.adminUsername)
  await page.getByLabel('密码').fill(seed.adminPassword)
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByRole('link', { name: '概览' })).toBeVisible({ timeout: 20000 })
  return page
}
