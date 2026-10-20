import { createI18n } from 'vue-i18n'
import zhCN from './zh-CN'

export const i18n = createI18n({
  legacy: false,
  locale: 'zh-CN',
  fallbackLocale: 'zh-CN',
  messages: { 'zh-CN': zhCN },
  missingWarn: false,
  fallbackWarn: false,
})

export const t = i18n.global.t

/** 按错误码显示本地化文案；没有对应文案时使用服务端返回的 message。 */
export function errorText(e: unknown): string {
  const err = e as { code?: number; message?: string }
  if (err?.code) {
    const key = `errors.${err.code}`
    const msg = t(key)
    if (msg !== key && !err.message) return msg
  }
  return err?.message || String(e)
}

const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

export function formatBytes(n: number, digits = 1): string {
  if (!Number.isFinite(n) || n < 0) return '-'
  let i = 0
  while (n >= 1024 && i < units.length - 1) {
    n /= 1024
    i++
  }
  return `${i === 0 ? n : n.toFixed(digits)} ${units[i]}`
}

export function formatRate(n: number): string {
  return `${formatBytes(n)}/s`
}

export function formatTime(ms: number | string | null | undefined, withSeconds = false): string {
  if (ms === null || ms === undefined || ms === '') return '-'
  const d = new Date(ms)
  if (Number.isNaN(d.getTime())) return '-'
  const p = (x: number) => String(x).padStart(2, '0')
  const s = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
  return withSeconds ? `${s}:${p(d.getSeconds())}` : s
}

export function formatDuration(sec: number): string {
  sec = Math.floor(sec)
  const d = Math.floor(sec / 86400)
  const h = Math.floor((sec % 86400) / 3600)
  const m = Math.floor((sec % 3600) / 60)
  if (d > 0) return t('time.dh', { d, h })
  if (h > 0) return t('time.hm', { h, m })
  return t('time.m', { m: Math.max(m, 0) })
}
