import { expect, type Page } from '@playwright/test'

export const USER = process.env.SD_USER ?? 'admin'
export const PASS = process.env.SD_PASS ?? ''

export async function login(page: Page, shell: 'desktop' | 'admin' = 'desktop') {
  await page.goto('/login')
  await page.locator('.lock').click({ position: { x: 10, y: 10 } })
  await page.getByTestId('login-username').locator('input').fill(USER)
  await page.getByTestId('login-password').locator('input').fill(PASS)
  await page.getByTestId('login-submit').click()
  await page.waitForURL(shell === 'desktop' ? /\/desktop/ : /\/admin/)
  // 等待桌面状态恢复完成
  await page.waitForLoadState('networkidle')
}

/** 通过开始菜单打开应用。 */
export async function openApp(page: Page, id: string) {
  await page.getByTestId('tb-start').click()
  await page.getByTestId('start-app-' + id).click()
  const win = page.getByTestId('window-' + id).last()
  await expect(win).toBeVisible()
  return win
}

/** 在当前桌面上关闭所有窗口。 */
export async function closeAll(page: Page) {
  for (;;) {
    const btn = page.getByTestId('win-close').first()
    if (!(await btn.isVisible().catch(() => false))) break
    // 窗口可能互相遮挡，直接派发点击事件
    await btn.dispatchEvent('click')
    await page.waitForTimeout(200)
  }
}

export function collectErrors(page: Page) {
  const errors: string[] = []
  page.on('pageerror', (e) => errors.push(e.message))
  page.on('console', (m) => {
    if (m.type() === 'error' && !/Failed to load resource|WebSocket/.test(m.text())) errors.push(m.text())
  })
  return errors
}
