import { describe, expect, it } from 'vitest'
import zh from './zh-CN'
import { apps } from '@/apps/registry'

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

  it('每个应用都有名称', () => {
    expect(apps.filter((a) => !has(a.title)).map((a) => a.id)).toEqual([])
  })
})
