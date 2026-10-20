import { test } from '@playwright/test'
import { closeAll, login, openApp } from './helpers'

// 截图用于人工检查界面，默认不运行（设置 SD_SHOTS 目录时运行）
test.skip(!process.env.SD_SHOTS, 'SD_SHOTS 未设置')

test('截图', async ({ page }) => {
  const dir = process.env.SD_SHOTS!
  await login(page)
  await closeAll(page)
  await page.screenshot({ path: `${dir}/01-desktop.png` })
  await page.getByTestId('tb-start').click()
  await page.screenshot({ path: `${dir}/02-start.png` })
  await page.keyboard.press('Escape')
  const ex = await openApp(page, 'explorer')
  await ex.getByTestId('explorer-address-edit').click()
  await ex.getByTestId('explorer-address').locator('input').fill('/etc')
  await page.keyboard.press('Enter')
  await page.waitForTimeout(500)
  await page.screenshot({ path: `${dir}/03-explorer.png` })
  await openApp(page, 'monitor')
  await page.waitForTimeout(2500)
  await page.screenshot({ path: `${dir}/04-monitor.png` })
  await closeAll(page)
  await openApp(page, 'terminal')
  await page.waitForTimeout(800)
  await page.keyboard.type('ls -la /\n')
  await page.waitForTimeout(500)
  await page.screenshot({ path: `${dir}/05-terminal.png` })
  await closeAll(page)
  await openApp(page, 'processes')
  await page.waitForTimeout(1000)
  await page.screenshot({ path: `${dir}/06-processes.png` })
  await closeAll(page)
  const st = await openApp(page, 'settings')
  await st.getByTestId('theme-dark').click()
  await openApp(page, 'users')
  await page.waitForTimeout(500)
  await page.screenshot({ path: `${dir}/07-dark-users.png` })
  await closeAll(page)
  const st2 = await openApp(page, 'settings')
  await st2.getByText('跟随系统').first().click()
  await closeAll(page)
  await page.goto('/admin/audit')
  await page.waitForTimeout(800)
  await page.screenshot({ path: `${dir}/08-admin-audit.png` })
  // M3：Docker、应用商店、计划任务、备份
  await page.goto('/desktop')
  await page.waitForLoadState('networkidle')
  await closeAll(page)
  const shots: [string, string][] = [
    ['docker', '09-docker'],
    ['appstore', '10-appstore'],
    ['cron', '11-cron'],
    ['backup', '12-backup'],
  ]
  for (const [id, name] of shots) {
    await openApp(page, id)
    await page.waitForTimeout(1500)
    await page.screenshot({ path: `${dir}/${name}.png` })
    await closeAll(page)
  }
})
