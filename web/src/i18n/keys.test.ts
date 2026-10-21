import { describe, expect, it } from 'vitest'
import zh from './zh-CN'
import { apps } from '@/apps/registry'
import { createI18n } from 'vue-i18n'

// 代码中写死的文案键（t('a.b')）都要在中文文案里存在，避免界面上直接显示键名
const sources = import.meta.glob(['../**/*.vue', '../**/*.ts', '!../**/*.test.ts'], { query: '?raw', import: 'default', eager: true }) as Record<string, string>

function has(key: string) {
  let o: unknown = zh
  for (const p of key.split('.')) {
    if (o === null || typeof o !== 'object' || !(p in o)) return false
    o = (o as Record<string, unknown>)[p]
  }
  return typeof o === 'string'
}

describe('中文文案', () => {
  it('代码引用的文案键都存在', () => {
    expect(Object.keys(sources).length).toBeGreaterThan(50)
    const missing: string[] = []
    for (const [file, src] of Object.entries(sources)) {
      for (const m of src.matchAll(/\bt\(\s*'([a-zA-Z0-9_]+(?:\.[a-zA-Z0-9_-]+)+)'\s*[,)]/g)) if (!has(m[1])) missing.push(`${file}: ${m[1]}`)
    }
    expect(missing).toEqual([])
  })

  it('所有文案都能编译（@、|、{ } 等是特殊字符）', () => {
    const errors: string[] = []
    const i18n = createI18n({ legacy: false, locale: 'zh-CN', messages: { 'zh-CN': zh }, missingWarn: false, fallbackWarn: false, warnHtmlMessage: false })
    const walk = (o: Record<string, unknown>, prefix: string) => {
      for (const [k, v] of Object.entries(o)) {
        const key = prefix + k
        if (typeof v === 'string') {
          try {
            i18n.global.t(key, {})
          } catch (e) {
            errors.push(`${key}: ${String(e)}`)
          }
        } else if (v && typeof v === 'object') walk(v as Record<string, unknown>, key + '.')
      }
    }
    walk(zh, '')
    expect(errors).toEqual([])
  })

  it('每个应用都有名称', () => {
    expect(apps.filter((a) => !has(a.title)).map((a) => a.id)).toEqual([])
  })
})
