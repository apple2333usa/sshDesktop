import { expect, test } from '@playwright/test'
import { collectErrors, login } from './helpers'

test('手机进入后台外观，菜单可切换页面', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page, 'admin')
  await page.getByTestId('admin-drawer').click()
  await page.getByRole('menuitem', { name: '文件资源管理器' }).click()
  await expect(page.getByTestId('explorer')).toBeVisible()
  await page.getByTestId('admin-drawer').click()
  await page.getByRole('menuitem', { name: '监控', exact: true }).click()
  await expect(page.getByTestId('card-cpu')).toBeVisible()
  expect(errors).toEqual([])
})
