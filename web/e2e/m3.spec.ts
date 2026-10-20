// M3：Docker（容器、Compose）→ 应用商店（本机目录源安装、卸载）→ 计划任务（立即执行看输出）→ 备份（本机目标、备份、恢复到其他目录）。
// 需要面板和测试在同一台机器上运行（用来准备测试文件和检查恢复结果），面板以 root 运行且装有 Docker；
// 设置 SD_M3=1 启用。会创建并清理名字以 e2e- 开头的容器、项目、应用、任务和备份。
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { expect, test, type Page } from '@playwright/test'
import { closeAll, collectErrors, login, openApp, PASS } from './helpers'

const ID = Date.now().toString(36)
const NAME = 'e2e-' + ID
const TMP = fs.mkdtempSync(path.join(os.tmpdir(), 'sd-e2e-'))

test.describe.configure({ mode: 'serial', timeout: 240_000 })
test.skip(!process.env.SD_M3, '没有设置 SD_M3=1')
test.afterAll(() => fs.rmSync(TMP, { recursive: true, force: true }))

const tab = (win: ReturnType<Page['getByTestId']>, name: string) => win.locator('.n-tabs-tab', { hasText: name }).first().click()

async function confirm(page: Page, password = false) {
  if (password) await page.getByTestId('danger-password').locator('input').fill(PASS)
  await page.getByTestId('danger-confirm').click()
}

async function taskDone(page: Page) {
  const log = page.getByTestId('task-log')
  await expect(log.getByTestId('task-status')).toHaveText('已完成', { timeout: 120_000 })
  await log.locator('.n-card-header__close').click()
}

test('Docker：创建、停止、删除容器，新建并删除 Compose 项目', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'docker')
  await tab(win, '容器')
  await win.getByTestId('ct-create').click()
  await page.getByTestId('ct-image').locator('input').fill('alpine:3')
  await page.getByTestId('ct-name').locator('input').fill(NAME)
  await page.locator('.n-form-item', { hasText: '启动命令' }).locator('input').fill('sleep 300')
  await page.getByTestId('ct-create-ok').click()
  await taskDone(page)
  const row = win.getByTestId('ct-' + NAME)
  await expect(row).toContainText('运行中', { timeout: 120_000 })
  await win.getByTestId(`ct-stop-${NAME}`).click()
  await confirm(page)
  await expect(row).toContainText('已停止', { timeout: 30_000 })
  await win.getByTestId('ct-more-' + NAME).click()
  await page.locator('.n-dropdown-option', { hasText: /^删除$/ }).click()
  await confirm(page)
  await expect(row).toBeHidden()

  await tab(win, 'Compose')
  await win.getByTestId('cp-create').click()
  await page.getByTestId('cp-name').locator('input').fill(NAME)
  await page.getByTestId('cp-yaml').locator('textarea').fill(`services:\n  app:\n    image: alpine:3\n    command: sleep 300\n`)
  await page.getByTestId('cp-create-ok').click()
  const prow = win.getByTestId('cp-' + NAME)
  await taskDone(page)
  await expect(prow).toContainText('运行中', { timeout: 60_000 })
  await win.getByTestId('cp-more-' + NAME).click()
  await page.locator('.n-dropdown-option', { hasText: /^删除$/ }).click()
  await page.locator('.n-checkbox', { hasText: '同时删除项目目录' }).click()
  await page.getByTestId('cp-delete-ok').click()
  await confirm(page, true)
  await expect(prow).toBeHidden({ timeout: 60_000 })
  await closeAll(page)
  expect(errors).toEqual([])
})

test('应用商店：从本机目录源安装应用，停止后卸载', async ({ page }) => {
  const errors = collectErrors(page)
  // 准备一个本机目录源：demo 应用，一个版本
  const vd = path.join(TMP, 'apps', 'e2edemo', '1.0.0')
  fs.mkdirSync(vd, { recursive: true })
  fs.writeFileSync(path.join(TMP, 'apps', 'e2edemo', 'data.yml'), 'name: E2E Demo\nadditionalProperties:\n  key: e2edemo\n  shortDescZh: 端到端测试\n')
  fs.writeFileSync(
    path.join(vd, 'data.yml'),
    'additionalProperties:\n  formFields:\n    - envKey: PANEL_APP_PORT_HTTP\n      labelZh: 端口\n      type: number\n      default: 18097\n      required: true\n      rule: paramPort\n',
  )
  fs.writeFileSync(
    path.join(vd, 'docker-compose.yml'),
    'services:\n  demo:\n    image: alpine:3\n    container_name: ${CONTAINER_NAME}\n    command: sleep 300\n    ports:\n      - ${PANEL_APP_PORT_HTTP}:80\n    networks:\n      - 1panel-network\nnetworks:\n  1panel-network:\n    external: true\n',
  )
  await login(page)
  await closeAll(page)
  const hdr = { 'X-Requested-With': 'XMLHttpRequest' }
  const old = (await (await page.request.get('/api/v1/nodes/local/appstore/sources')).json()) as { data: { id: string }[] }
  old.data = old.data.filter((x) => x.id !== 'e2e')
  const r = await page.request.put('/api/v1/nodes/local/appstore/sources', {
    headers: hdr,
    data: [...old.data, { id: 'e2e', name: 'e2e', type: 'dir', url: path.join(TMP, 'apps'), enabled: true }],
  })
  expect(r.ok(), await r.text()).toBeTruthy()
  try {
    const win = await openApp(page, 'appstore')
    await win.getByTestId('app-sync').click()
    await taskDone(page)
    await win.getByTestId('app-search').locator('input').fill('e2edemo')
    await expect(win.getByTestId('app-e2edemo')).toBeVisible({ timeout: 60_000 })
    await win.getByTestId('app-install-e2edemo').click()
    const dlg = page.getByTestId('app-install')
    await dlg.getByTestId('app-name').locator('input').fill(NAME)
    // 默认允许外部访问，这里改为只监听本机
    await dlg.getByTestId('app-external').click()
    await dlg.getByTestId('app-install-ok').click()
    await taskDone(page)
    await tab(win, '已安装')
    await expect(win.getByTestId('inst-state-' + NAME)).toContainText('运行中', { timeout: 30_000 })
    await expect(win.getByTestId('inst-' + NAME)).toContainText('18097（仅本机）')
    await win.getByTestId('inst-stop-' + NAME).click()
    await confirm(page)
    await taskDone(page)
    await expect(win.getByTestId('inst-state-' + NAME)).toContainText('已停止', { timeout: 30_000 })
    await win.getByTestId('inst-more-' + NAME).click()
    await page.locator('.n-dropdown-option', { hasText: /^卸载$/ }).click()
    await page.getByTestId('inst-delete-data').click()
    await page.getByTestId('inst-uninstall-ok').click()
    await confirm(page, true)
    await taskDone(page)
    await expect(win.getByTestId('inst-' + NAME)).toBeHidden({ timeout: 30_000 })
  } finally {
    await page.request.put('/api/v1/nodes/local/appstore/sources', { headers: hdr, data: old.data })
  }
  await closeAll(page)
  expect(errors).toEqual([])
})

test('计划任务：新建 Shell 任务，立即执行并查看输出，然后删除', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'cron')
  await win.getByTestId('cron-add').click()
  await page.getByTestId('cron-name').locator('input').fill(NAME)
  await page.getByTestId('cron-script').locator('textarea').fill(`#!/bin/bash\necho "hello ${NAME}"\n`)
  await page.getByTestId('cron-save').click()
  const row = win.locator('tbody tr', { hasText: NAME })
  await expect(row).toBeVisible()
  await row.locator('button', { hasText: '立即执行' }).click()
  await expect(page.getByTestId('cron-output')).toContainText('hello ' + NAME, { timeout: 30_000 })
  await page.locator('.n-drawer .n-base-close').click()
  await expect(row).toContainText('成功')
  await row.locator('button', { hasText: '删除' }).click()
  await confirm(page)
  await expect(row).toBeHidden()
  await closeAll(page)
  expect(errors).toEqual([])
})

test('备份：本机目标、加密备份目录、恢复到其他目录，再清理', async ({ page }) => {
  const errors = collectErrors(page)
  const src = path.join(TMP, 'src')
  fs.mkdirSync(path.join(src, 'sub'), { recursive: true })
  fs.writeFileSync(path.join(src, 'sub', 'a.txt'), 'backup ' + NAME)
  fs.writeFileSync(path.join(src, 'skip.log'), 'x')
  const store = path.join(TMP, 'store')
  const dest = path.join(TMP, 'restored')

  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'backup')
  await tab(win, '备份目标')
  await win.getByTestId('target-add').click()
  await page.getByTestId('target-name').locator('input').fill(NAME)
  await page.getByTestId('target-path').locator('input').fill(store)
  await page.getByTestId('target-form-test').click()
  await expect(page.locator('.n-message', { hasText: '连接正常' })).toBeVisible()
  await page.getByTestId('target-save').click()
  const trow = win.locator('tr[data-testid^="target-"]', { hasText: NAME })
  await expect(trow).toBeVisible()

  await tab(win, '备份计划')
  await win.getByTestId('plan-add').click()
  await page.getByTestId('plan-name').locator('input').fill(NAME)
  await page.getByTestId('plan-source').locator('input').fill(src)
  await page.locator('.n-form-item', { hasText: '排除' }).locator('textarea').fill('*.log')
  await page.getByTestId('plan-targets').click()
  await page.locator('.n-base-select-option', { hasText: NAME }).click()
  await expect(page.getByTestId('plan-targets')).toContainText(NAME)
  await page.getByTestId('plan-name').click()
  await page.locator('.n-form-item', { hasText: '定时' }).getByTestId('cron-schedule').locator('input').fill('')
  await page.getByTestId('plan-encrypt').click()
  await page.getByTestId('plan-password').locator('input').fill('e2e-backup-pass')
  await page.getByTestId('plan-save').click()
  const prow = win.locator('tr[data-testid^="plan-"]', { hasText: NAME })
  await expect(prow).toContainText('仅手动')
  await prow.locator('button', { hasText: '立即备份' }).click()
  await taskDone(page)
  await expect(prow).toContainText('成功')

  await prow.locator('button', { hasText: '备份记录' }).click()
  const rrow = win.locator('tr[data-testid^="rec-"]', { hasText: NAME }).first()
  await expect(rrow).toContainText('已加密')
  await rrow.locator('button', { hasText: '恢复' }).click()
  await page.locator('.n-radio', { hasText: '其他目录' }).click()
  await page.getByTestId('restore-dest').locator('input').fill(dest)
  await page.getByTestId('restore-ok').click()
  await confirm(page, true)
  await taskDone(page)
  expect(fs.readFileSync(path.join(dest, 'sub', 'a.txt'), 'utf8')).toBe('backup ' + NAME)
  expect(fs.existsSync(path.join(dest, 'skip.log'))).toBe(false)

  await rrow.locator('button', { hasText: '删除' }).click()
  await confirm(page, true)
  await expect(win.locator('tr[data-testid^="rec-"]', { hasText: NAME })).toHaveCount(0)
  await tab(win, '备份计划')
  await prow.locator('button', { hasText: '删除' }).click()
  await confirm(page)
  await expect(prow).toBeHidden()
  await tab(win, '备份目标')
  await trow.locator('button', { hasText: '删除' }).click()
  await confirm(page)
  await expect(trow).toBeHidden()
  await closeAll(page)
  expect(errors).toEqual([])
})
