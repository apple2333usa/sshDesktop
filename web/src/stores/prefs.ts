import { defineStore } from 'pinia'
import { computed, reactive, ref, watch } from 'vue'

export type ThemeMode = 'auto' | 'light' | 'dark'
export type Appearance = 'auto' | 'desktop' | 'admin'

export interface Snippet {
  name: string
  command: string
}

export interface Prefs {
  theme: ThemeMode
  wallpaper: string
  appearance: Appearance
  pinned: string[]
  terminal: { fontSize: number; scheme: 'dark' | 'light'; snippets: Snippet[] }
  explorer: { view: 'details' | 'list' | 'icons'; showHidden: boolean }
  desktopDir: string
}

export function defaultPrefs(): Prefs {
  return {
    theme: 'auto',
    wallpaper: 'bloom',
    appearance: 'auto',
    pinned: ['explorer', 'terminal', 'servers', 'monitor', 'processes', 'settings'],
    terminal: {
      fontSize: 14,
      scheme: 'dark',
      snippets: [
        { name: '磁盘使用', command: 'df -h' },
        { name: '内存使用', command: 'free -h' },
        { name: '监听端口', command: 'ss -lntup' },
      ],
    },
    explorer: { view: 'details', showHidden: false },
    desktopDir: '/root/Desktop',
  }
}

const LS_KEY = 'sd.prefs'

function readLocal(): Partial<Prefs> {
  try {
    return JSON.parse(localStorage.getItem(LS_KEY) ?? '{}')
  } catch {
    return {}
  }
}

export const usePrefs = defineStore('prefs', () => {
  const p = reactive<Prefs>({ ...defaultPrefs(), ...readLocal() })
  const systemDark = ref(window.matchMedia?.('(prefers-color-scheme: dark)').matches ?? false)
  window.matchMedia?.('(prefers-color-scheme: dark)').addEventListener?.('change', (e) => (systemDark.value = e.matches))

  const dark = computed(() => p.theme === 'dark' || (p.theme === 'auto' && systemDark.value))

  watch(
    dark,
    (d) => {
      document.documentElement.dataset.theme = d ? 'dark' : 'light'
    },
    { immediate: true },
  )

  // 主题和外观在本机也存一份，刷新时不闪烁
  watch(
    () => ({ theme: p.theme, appearance: p.appearance, wallpaper: p.wallpaper }),
    (v) => {
      try {
        localStorage.setItem(LS_KEY, JSON.stringify(v))
      } catch {
        /* 存储不可用时忽略 */
      }
    },
    { deep: true },
  )

  function apply(saved: Partial<Prefs> | undefined) {
    if (!saved) return
    const d = defaultPrefs()
    Object.assign(p, {
      ...d,
      ...saved,
      terminal: { ...d.terminal, ...(saved.terminal ?? {}) },
      explorer: { ...d.explorer, ...(saved.explorer ?? {}) },
    })
  }

  return { p, dark, apply }
})

/** 小于 1024 px 或触屏设备默认使用后台外观。 */
export function isCompactDevice() {
  return window.innerWidth < 1024 || (window.matchMedia?.('(pointer: coarse)').matches ?? false)
}
