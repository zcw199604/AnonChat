import base from './playwright.config'
import { defineConfig } from '@playwright/test'

/**
 * 视觉截图专用配置：只匹配 e2e/visual.capture.ts，不影响常规 E2E 的 testMatch。
 * 用法：pnpm capture:visual
 */
export default defineConfig({
  ...base,
  testMatch: /visual\.capture\.ts$/,
  timeout: 120_000,
})
