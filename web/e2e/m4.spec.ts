// M4：网站（宿主机 Nginx、反向代理、上传证书启用 HTTPS）→ 防火墙（放行、删除需确认）→ SSH 设置（改端口后撤销、授权密钥）
// → 环境体检 → 面板访问方式（切换到另一个本机端口再切回来）。
// 需要面板和测试在同一台机器上运行，面板以 root 运行，装有 Nginx（80 端口）、openssl、ssh-keygen；设置 SD_M4=1 启用。
// SD_M4_PORT 指定面板临时切换到的端口（默认 33398）。会创建并清理名字以 e2e- 开头的站点、证书、防火墙规则和授权密钥。
import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { expect, test, type Locator, type Page } from '@playwright/test'
import { closeAll, collectErrors, login, openApp, PASS } from './helpers'

const ID = Date.now().toString(36)
const NAME = 'e2e-' + ID
const DOMAIN = NAME + '.test'
const TMP = fs.mkdtempSync(path.join(os.tmpdir(), 'sd-e2e-'))
const ALT_PORT = Number(process.env.SD_M4_PORT ?? 33398)
const FW_PORT = String(40000 + (Date.now() % 9000))

test.describe.configure({ mode: 'serial', timeout: 120_000 })
test.skip(!process.env.SD_M4, '没有设置 SD_M4=1')
test.afterAll(() => fs.rmSync(TMP, { recursive: true, force: true }))

const tab = (win: Locator, name: string) => win.locator('.n-tabs-tab', { hasText: name }).first().click()

async function confirm(page: Page, password = false) {
  if (password) await page.getByTestId('danger-password').locator('input').fill(PASS)
  await page.getByTestId('danger-confirm').click()
}

async function taskDone(page: Page) {
  const log = page.getByTestId('task-log')
  await expect(log.getByTestId('task-status')).toHaveText('已完成', { timeout: 120_000 })
  await log.locator('.n-card-header__close').click()
}

/** 通过本机 80 端口访问站点（带 Host 头），返回状态码；body 为 true 时返回响应正文。 */
function viaNginx(scheme: 'http' | 'https', body = false) {
  const out = body ? [] : ['-o', '/dev/null', '-w', '%{http_code}']
  const args = ['-sS', '--noproxy', '*', '-k', ...out, '--resolve', `${DOMAIN}:${scheme === 'http' ? 80 : 443}:127.0.0.1`, `${scheme}://${DOMAIN}/`]
  return execFileSync('curl', args, { encoding: 'utf8' })
}

test('网站：接管 Nginx，新建反向代理，上传证书启用 HTTPS，删除', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'websites')
  const setup = win.getByTestId('web-setup-host')
  await expect(setup.or(win.getByTestId('web-running'))).toBeVisible()
  if (await setup.isVisible()) {
    await setup.click()
    await confirm(page)
    await taskDone(page)
  }
  await expect(win.getByTestId('web-running')).toHaveText('运行中')

  // 反向代理到面板自己
  await win.getByTestId('site-add').click()
  await page.getByTestId('site-domains').locator('textarea').fill(DOMAIN)
  await page.getByTestId('site-name').locator('input').fill(NAME)
  await page.getByTestId('site-proxy').locator('input').fill(new URL(page.url()).origin)
  await page.getByTestId('site-save').click()
  const row = win.getByTestId('site-' + NAME)
  await expect(row).toContainText(DOMAIN)
  await expect(row).toContainText('仅 HTTP')
  await expect.poll(() => viaNginx('http')).toBe('200')

  // 上传自签证书
  execFileSync('openssl', ['req', '-x509', '-newkey', 'ec', '-pkeyopt', 'ec_paramgen_curve:P-256', '-nodes', '-days', '30', '-subj', '/CN=' + DOMAIN,
    '-addext', 'subjectAltName=DNS:' + DOMAIN, '-keyout', path.join(TMP, 'key.pem'), '-out', path.join(TMP, 'cert.pem')], { stdio: 'ignore' })
  await tab(win, '证书')
  await win.getByTestId('cert-upload').click()
  await page.getByTestId('cert-pem').locator('textarea').fill(fs.readFileSync(path.join(TMP, 'cert.pem'), 'utf8'))
  await page.getByTestId('key-pem').locator('textarea').fill(fs.readFileSync(path.join(TMP, 'key.pem'), 'utf8'))
  await page.getByTestId('cert-upload-ok').click()
  const certRow = win.locator('tr', { hasText: DOMAIN })
  await expect(certRow).toContainText('有效')

  // 站点启用 HTTPS
  await tab(win, '站点')
  await win.getByTestId('site-edit-' + NAME).click()
  await page.getByTestId('site-https').click()
  await page.getByTestId('site-cert').click()
  await page.locator('.n-base-select-option', { hasText: DOMAIN }).click()
  await page.getByTestId('site-save').click()
  await expect(row).toContainText('剩 29 天')
  await expect.poll(() => viaNginx('http')).toBe('301')
  await expect.poll(() => viaNginx('https')).toBe('200')

  // 证书正在使用，不能删除
  await tab(win, '证书')
  await expect(certRow).toContainText(NAME)

  // 删除站点，再删除证书
  await tab(win, '站点')
  await win.getByTestId('site-more-' + NAME).click()
  await page.locator('.n-dropdown-option', { hasText: /^删除$/ }).click()
  await confirm(page)
  await expect(row).toHaveCount(0)
  // 默认站点可能也返回 200（例如 Nginx 欢迎页），按正文判断不再代理到面板
  await expect.poll(() => viaNginx('http', true)).not.toContain('<title>sshDesktop</title>')
  await tab(win, '证书')
  await certRow.locator('button', { hasText: '…' }).click()
  await page.locator('.n-dropdown-option', { hasText: /^删除$/ }).click()
  await confirm(page)
  await expect(certRow).toHaveCount(0)
  expect(errors).toEqual([])
})

test('防火墙：放行端口，删除规则', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'firewall')
  await expect(win.getByTestId('fw-add').or(win.getByText('没有找到防火墙'))).toBeVisible()
  test.skip(!(await win.getByTestId('fw-add').isVisible()), '没有可用的防火墙')
  await win.getByTestId('fw-add').click()
  await page.getByTestId('fw-port').locator('input').fill(FW_PORT)
  await page.getByTestId('fw-save').click()
  const row = win.getByTestId('fw-rule').filter({ hasText: FW_PORT })
  await expect(row.first()).toBeVisible()
  await row.first().locator('button', { hasText: '删除' }).click()
  await confirm(page)
  // 防火墙启用时删除规则需要确认
  const bar = win.getByTestId('pending-bar')
  await expect(bar.or(win.getByTestId('fw-rule').first())).toBeVisible()
  if (await bar.isVisible()) {
    await bar.getByTestId('pending-confirm').click()
    await expect(bar).toHaveCount(0)
  }
  await expect(row).toHaveCount(0)
  expect(errors).toEqual([])
})

test('SSH 设置：改端口后撤销，添加和删除授权密钥', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'sshd')
  await tab(win, '设置')
  const port = win.getByTestId('sshd-port').locator('input')
  await expect(port).not.toHaveValue('')
  const old = await port.inputValue()
  await port.fill(String(Number(old) + 1))
  await port.blur()
  await win.getByTestId('sshd-save').click()
  await confirm(page, true)
  const bar = win.getByTestId('pending-bar')
  await expect(bar).toBeVisible()
  await bar.getByTestId('pending-revert').click()
  await expect(bar).toHaveCount(0, { timeout: 30_000 })
  await expect(port).toHaveValue(old)

  execFileSync('ssh-keygen', ['-q', '-t', 'ed25519', '-N', '', '-C', NAME, '-f', path.join(TMP, 'id')])
  await tab(win, '授权密钥')
  await win.getByTestId('sshd-add-key').click()
  await page.getByTestId('sshd-key').locator('textarea').fill(fs.readFileSync(path.join(TMP, 'id.pub'), 'utf8'))
  await page.getByTestId('sshd-key-save').click()
  const row = win.locator('tr', { hasText: NAME })
  await expect(row).toContainText('ssh-ed25519')
  await row.locator('button', { hasText: '删除' }).click()
  await confirm(page, true)
  await expect(row).toHaveCount(0)

  await tab(win, '登录记录')
  await expect(win.locator('.sd-toolbar').last()).toContainText(/最近成功|没有找到/)
  expect(errors).toEqual([])
})

test('环境体检', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const win = await openApp(page, 'inspect')
  await win.getByTestId('inspect-run').click()
  await expect(win).toContainText('检查于')
  await expect(win).toContainText('Nginx / OpenResty')
  await expect(win).toContainText('SSH')
  expect(errors).toEqual([])
})

test('面板访问方式：切换到另一个端口并在新地址确认，再切回来', async ({ page }) => {
  const errors = collectErrors(page)
  await login(page)
  await closeAll(page)
  const origin = new URL(page.url())
  const switchTo = async (port: number) => {
    const win = await openApp(page, 'settings')
    await win.getByTestId('settings-panel').click()
    const card = win.getByTestId('panel-access')
    await card.getByTestId('access-port').locator('input').fill(String(port))
    await card.getByTestId('access-port').locator('input').blur()
    await card.getByTestId('access-apply').click()
    await confirm(page, true)
    await page.getByTestId('access-go').click()
    await page.waitForURL((u) => u.port === String(port))
    await expect(page.locator('.n-dialog')).toContainText('访问方式已切换')
    await page.locator('.n-dialog').getByRole('button', { name: '确定' }).click()
    await page.waitForLoadState('networkidle')
    await closeAll(page)
  }
  await switchTo(ALT_PORT)
  expect(page.url()).not.toContain('confirm-access')
  // 原端口已经关闭
  await expect.poll(async () => page.request.get(origin.origin + '/api/v1/auth/me').then(() => 'open', () => 'closed')).toBe('closed')
  await switchTo(Number(origin.port))
  expect(errors).toEqual([])
})
