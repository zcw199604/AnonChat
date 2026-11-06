import { expect, type Browser, type Page } from '@playwright/test'

/** 打开首页并等待匿名身份初始化与 WS 连接完成。 */
export async function openHome(browser: Browser): Promise<Page> {
  const ctx = await browser.newContext()
  const page = await ctx.newPage()
  await page.goto('/')
  await expect(page.getByTestId('ws-connected')).toBeVisible({ timeout: 30000 })
  return page
}

export async function startMatch(page: Page): Promise<void> {
  await page.getByRole('link', { name: '随机匹配' }).click()
  await page.getByRole('button', { name: '开始匹配' }).click()
}

export async function sendChat(page: Page, text: string): Promise<void> {
  const input = page.getByLabel('消息输入框')
  await input.fill(text)
  await input.press('Enter')
}
