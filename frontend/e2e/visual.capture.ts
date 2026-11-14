import { test, expect } from '@playwright/test'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { sendChat, startMatch } from './helpers'

/** 两种主题 × 桌面/移动 × 首页/等待/聊天/菜单/结束，截图不入库。 */
const OUT_DIR = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '__screenshots__')
const VIEWPORTS = [
  { name: 'desktop', width: 1280, height: 800 },
  { name: 'mobile', width: 390, height: 844 },
]

for (const theme of ['light', 'dark'] as const) {
  for (const vp of VIEWPORTS) {
    test(`白天夜晚匹配与聊天截图 · ${theme} · ${vp.name}`, async ({ browser }) => {
      const contexts = await Promise.all([0, 1].map(async () => {
        const ctx = await browser.newContext({ viewport: { width: vp.width, height: vp.height }, colorScheme: theme })
        await ctx.addInitScript((preference) => localStorage.setItem('anonchat.theme', preference), theme)
        return ctx
      }))
      try {
        const [a, b] = await Promise.all(contexts.map((ctx) => ctx.newPage()))
        await Promise.all([a, b].map(async (page) => {
          await page.goto('/')
          await expect(page.getByTestId('ws-connected')).toBeVisible({ timeout: 30_000 })
          await expect(page.locator('html')).toHaveAttribute('data-theme', theme)
          await page.evaluate(() => document.fonts.ready)
        }))
        const filename = (state: string) => path.join(OUT_DIR, `${state}-${theme}-${vp.name}.png`)
        // 等待已声明的主题背景过渡完成，避免截到过渡中间帧。
        await a.waitForTimeout(350)
        await a.screenshot({ path: filename('home'), fullPage: true })

        await startMatch(a)
        await expect(a.getByText('正在寻找有趣的人')).toBeVisible({ timeout: 20_000 })
        await a.screenshot({ path: filename('match') })
        await startMatch(b)
        for (const page of [a, b]) {
          await expect(page.getByLabel('消息输入框')).toBeVisible({ timeout: 20_000 })
          await expect(page.getByRole('button', { name: '进入聊天' })).toHaveCount(0)
        }
        await sendChat(a, '你好，很高兴遇见你！')
        await expect(b.getByText('你好，很高兴遇见你！')).toBeVisible()
        await sendChat(b, '我也是，今天过得怎么样？')
        await expect(a.getByText('我也是，今天过得怎么样？')).toBeVisible()
        await a.waitForTimeout(400)
        await a.screenshot({ path: filename('chat') })

        await a.getByRole('button', { name: '更多操作' }).click()
        await expect(a.getByRole('dialog', { name: '更多操作' })).toBeVisible()
        await a.waitForTimeout(400)
        await a.screenshot({ path: filename('menu') })
        await a.getByRole('button', { name: '取消', exact: true }).click()

        await b.getByRole('button', { name: '返回', exact: true }).click()
        await b.getByRole('button', { name: '结束聊天', exact: true }).click()
        await expect(a.getByText('对方已离开', { exact: true })).toBeVisible({ timeout: 20_000 })
        await expect(a.getByText('对话已结束，聊天记录仍可往上翻看')).toBeVisible()
        await a.screenshot({ path: filename('ended') })
      } finally {
        await Promise.all(contexts.map((ctx) => ctx.close()))
      }
    })
  }
}
