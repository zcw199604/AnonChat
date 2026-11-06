import { test, expect } from '@playwright/test'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

/**
 * 视觉截图脚本：在桌面（1280×800）和移动端（390×844）下各截首页与匹配等待页。
 * 输出到 frontend/e2e/__screenshots__/（不入库）。
 * 文件名不匹配常规 E2E 的 testMatch，只在 --config e2e/visual.config.ts 下运行。
 */
const here = path.dirname(fileURLToPath(import.meta.url))
const OUT_DIR = path.resolve(here, '__screenshots__')

const VIEWPORTS = [
  { name: 'desktop', width: 1280, height: 800 },
  { name: 'mobile', width: 390, height: 844 },
]

for (const vp of VIEWPORTS) {
  test(`首页与匹配等待页截图 · ${vp.name}`, async ({ browser }) => {
    const ctx = await browser.newContext({ viewport: { width: vp.width, height: vp.height } })
    const page = await ctx.newPage()

    await page.goto('/')
    await expect(page.getByTestId('ws-connected')).toBeVisible({ timeout: 30000 })
    await page.waitForTimeout(600)
    await page.screenshot({ path: path.join(OUT_DIR, `home-${vp.name}.png`), fullPage: true })

    await page.getByTestId('match-button').click()
    await expect(page.getByText('正在寻找…')).toBeVisible({ timeout: 20000 })
    await page.waitForTimeout(400)
    await page.screenshot({ path: path.join(OUT_DIR, `match-${vp.name}.png`) })

    await ctx.close()
  })
}
