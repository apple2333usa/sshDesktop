// 多服务器：添加远程服务器（指纹确认）→ 卡片墙 → 网络位置与跨服务器复制 → 远程终端 → 批量命令
// → 流量统计 → 服务监控 → 删除服务器。需要一台可以 SSH 登录的测试服务器（可以就是面板所在的机器）：
// SD_SSH_HOST、SD_SSH_PORT（默认 22）、SD_SSH_USER、SD_SSH_PASS；没有设置时跳过。
import { expect, test, type Page } from '@playwright/test'
import { closeAll, collectErrors, login, openApp, PASS } from './helpers'

const HOST = process.env.SD_SSH_HOST ?? ''
const PORT = process.env.SD_SSH_PORT ?? '22'
const SSH_USER = process.env.SD_SSH_USER ?? ''
const SSH_PASS = process.env.SD_SSH_PASS ?? ''
const NAME = 'e2e-' + Date.now().toString(36)

test.describe.configure({ mode: 'serial' })
test.skip(!HOST || !SSH_USER || !SSH_PASS, '没有设置 SD_SSH_HOST / SD_SSH_USER / SD_SSH_PASS')

let nodeId = ''

async function serverCard(page: Page) {
  const win = page.getByTestId('window-servers')
  const card = win.locator('.card', { hasText: NAME })
  return { win, card }
}

test('添加服务器：获取指纹、确认后保存，卡片显示在线', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'servers')
  await win.getByTestId('servers-add').click()
  const dlg = page.getByTestId('node-dialog')
  await dlg.getByTestId('node-name').locator('input').fill(NAME)
  await dlg.getByTestId('node-host').locator('input').fill(HOST)
  await dlg.getByTestId('node-port').locator('input').fill(PORT)
  await dlg.getByTestId('node-user').locator('input').fill(SSH_USER)
  // 没确认指纹不能保存
  await dlg.getByTestId('node-auth-password').click()
  await dlg.getByTestId('node-password').locator('input').fill(SSH_PASS)
  await dlg.getByTestId('node-save').click()
  await expect(page.getByTestId('node-error')).toContainText('指纹')
  await dlg.getByTestId('node-scan').click()
  await expect(dlg.getByTestId('node-fingerprint')).toContainText('SHA256:')
  await dlg.getByTestId('node-fp-confirm').click()
  await dlg.getByTestId('node-test').click()
  await expect(dlg.getByText('连接成功')).toBeVisible({ timeout: 30_000 })
  await dlg.getByTestId('node-save').click()
  await expect(dlg).toBeHidden({ timeout: 30_000 })

  const { card } = await serverCard(page)
  await expect(card).toBeVisible()
  await expect(card.getByTestId('server-state')).toHaveText('在线', { timeout: 30_000 })
  nodeId = ((await card.getAttribute('data-testid')) ?? '').replace('server-', '')
  expect(nodeId).toMatch(/^n[0-9a-f]{10}$/)
  // 监控数据（agent / 面板 / SSH 采集都可以）
  await expect(card.locator('.meters')).toBeVisible({ timeout: 30_000 })
  await expect(card.getByTestId('server-source')).toBeVisible()
  // 任务栏托盘显示节点状态
  await expect(page.getByTestId('tb-nodes')).toHaveText(/^\s*(\d+)\/\1\s*$/)
  await closeAll(page)
  expect(errors).toEqual([])
})

test('监控：切换到远程服务器，窗口标题显示节点标签', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'servers')
  const { card } = await serverCard(page)
  await card.dblclick()
  const mon = page.getByTestId('window-monitor')
  await expect(mon).toBeVisible()
  await expect(mon.getByTestId('win-node-tag')).toHaveText(NAME)
  await expect(mon.getByTestId('card-cpu')).toBeVisible({ timeout: 30_000 })
  await win.getByTestId('win-close').dispatchEvent('click')
  await closeAll(page)
  expect(errors).toEqual([])
})

test('资源管理器：网络位置，从远程服务器复制文件到本机', async ({ page }) => {
  const errors = collectErrors(page)
  const file = `sd-e2e-${Date.now()}.txt`
  await login(page)
  await closeAll(page)
  // 先用批量命令在远程家目录准备一个文件（同时验证批量命令）
  const batch = await openApp(page, 'batch')
  await batch.getByTestId('batch-node-local').locator('input').uncheck()
  await batch.getByTestId('batch-node-' + nodeId).locator('input').check()
  await batch.getByTestId('batch-command').locator('textarea').fill(`echo remote-$((6*7)) > ~/${file} && cat ~/${file}`)
  await batch.getByTestId('batch-run').click()
  await expect(page.getByTestId('danger-dialog')).toContainText(NAME)
  await page.getByTestId('danger-confirm').click()
  const res = batch.getByTestId('batch-result-' + nodeId)
  await expect(res.getByTestId('batch-output')).toContainText('remote-42', { timeout: 30_000 })
  await expect(res.getByTestId('batch-status')).toContainText('0')
  await closeAll(page)

  const win = await openApp(page, 'explorer')
  await win.getByTestId('nav-network').click()
  await expect(win.getByTestId('network-view')).toBeVisible()
  await win.getByTestId('network-' + nodeId).dblclick()
  await expect(win.getByTestId('win-node-tag')).toHaveText(NAME)
  await win.getByTestId('computer-home').dblclick()
  await expect(win.getByTestId('file-' + file)).toBeVisible()
  await win.getByTestId('file-' + file).click()
  await page.keyboard.press('Control+c')

  // 切回本机 /tmp 粘贴：跨服务器复制
  await win.getByTestId('nav-computer').click()
  await win.getByTestId('explorer-address-edit').click()
  await win.getByTestId('explorer-address').locator('input').fill('/tmp')
  await page.keyboard.press('Enter')
  await expect(win.getByTestId('win-node-tag')).toBeHidden()
  await win.getByTestId('explorer-paste').click()
  await expect(win.getByTestId('file-' + file)).toBeVisible({ timeout: 30_000 })
  await expect(win.getByTestId('file-' + file)).toContainText('10 B')
  // 清理本机副本
  await win.getByTestId('file-' + file).click()
  await page.keyboard.press('Shift+Delete')
  await page.getByTestId('danger-password').locator('input').fill(PASS)
  await page.getByTestId('danger-confirm').click()
  await expect(win.getByTestId('file-' + file)).toBeHidden()
  await closeAll(page)
  expect(errors).toEqual([])
})

test('终端：在远程服务器上新建标签页并执行命令', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'servers')
  const { card } = await serverCard(page)
  await card.getByTestId('server-menu').click()
  await page.getByText('终端', { exact: true }).last().click()
  await win.getByTestId('win-close').dispatchEvent('click')
  const term = page.getByTestId('window-terminal')
  await expect(term.getByTestId('win-node-tag')).toHaveText(NAME)
  await expect(term.locator('.xterm').first()).toBeVisible()
  await term.locator('.xterm').first().click()
  await page.waitForTimeout(800)
  await page.keyboard.type('echo "$(whoami)-$((40+2))"')
  await page.keyboard.press('Enter')
  await expect(term.locator('.xterm-rows')).toContainText(`${SSH_USER}-42`)
  await closeAll(page)
  expect(errors).toEqual([])
})

test('流量统计与服务监控页面', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const tr = await openApp(page, 'traffic')
  await expect(tr.getByTestId('traffic-row-local')).toBeVisible()
  await expect(tr.getByTestId('traffic-row-' + nodeId)).toBeVisible()
  await tr.getByTestId('traffic-row-' + nodeId).click()
  await tr.getByTestId('traffic-settings').click()
  await page.getByTestId('traffic-reset-day').locator('input').fill('15')
  await page.getByTestId('traffic-save').click()
  await expect(page.getByText('每月 15 日重置')).toBeVisible()
  await closeAll(page)

  const pr = await openApp(page, 'probes')
  await pr.getByTestId('probe-add').click()
  await page.getByText('TCP', { exact: true }).click()
  await page.getByTestId('probe-target').locator('input').fill(`${HOST}:${PORT}`)
  await page.getByTestId('probe-name').locator('input').fill(NAME + '-ssh')
  await page.getByTestId('probe-test').click()
  await expect(page.getByText(/^正常 ·/)).toBeVisible({ timeout: 20_000 })
  await page.getByTestId('probe-save').click()
  const item = pr.locator('.item', { hasText: NAME + '-ssh' })
  await expect(item).toBeVisible()
  await expect(item.locator('.dot.up')).toBeVisible({ timeout: 20_000 })
  // 删除这个探测
  await pr.getByRole('button', { name: '删除' }).click()
  await page.getByTestId('danger-confirm').click()
  await expect(item).toBeHidden()
  await closeAll(page)
  expect(errors).toEqual([])
})

test('删除服务器（二级确认）', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  await openApp(page, 'servers')
  const { card } = await serverCard(page)
  await card.getByTestId('server-menu').click()
  await page.getByText('删除', { exact: true }).last().click()
  await expect(page.getByTestId('danger-dialog')).toContainText(`${SSH_USER}@${HOST}:${PORT}`)
  await page.getByTestId('danger-password').locator('input').fill(PASS)
  await page.getByTestId('danger-confirm').click()
  await expect(card).toBeHidden()
  await closeAll(page)
  expect(errors).toEqual([])
})
