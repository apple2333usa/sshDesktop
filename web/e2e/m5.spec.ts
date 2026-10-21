// M5（稳定化）：设置中的面板升级卡片。不真正升级（需要 systemd 和网络），只检查版本信息和不能升级时的提示。
import { expect, test } from '@playwright/test'
import { closeAll, collectErrors, login, openApp } from './helpers'

test('设置 > 关于：显示当前版本、检查更新', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'settings')
  await win.getByTestId('settings-about').click()
  const card = win.getByTestId('panel-upgrade')
  await expect(card).toContainText('当前版本')
  // 面板没有由 systemd 运行时提示改用命令行（SD_NO_SYSTEMD），否则可以升级或已是最新
  if (process.env.SD_NO_SYSTEMD) await expect(card).toContainText('sshdesktop upgrade')
  await card.getByTestId('upgrade-check').click()
  await expect(card).toContainText(/检查于/)
  await closeAll(page)
  expect(errors).toEqual([])
})
