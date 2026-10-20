// 桌面状态（窗口、图标、个性化设置）保存在服务器上：变化后 2 秒防抖写入，登录后读取恢复。
import { watch, type WatchStopHandle } from 'vue'
import { api } from '@/api/http'
import { useDesktop } from './desktop'
import { usePrefs, type Prefs } from './prefs'

interface SavedState {
  version: 1
  prefs: Prefs
  desktop: ReturnType<ReturnType<typeof useDesktop>['serialize']>
}

let stop: WatchStopHandle | null = null
let timer: number | undefined
let loaded = false

export async function loadDesktopState() {
  const desktop = useDesktop()
  const prefs = usePrefs()
  try {
    const saved = await api.get<SavedState | null>('/desktop/state')
    if (saved) {
      prefs.apply(saved.prefs)
      desktop.restore(saved.desktop)
    }
  } catch {
    /* 读取失败时使用默认状态 */
  }
  loaded = true
  stop?.()
  stop = watch(
    () => [prefs.p, desktop.serialize()],
    () => {
      clearTimeout(timer)
      timer = window.setTimeout(save, 2000)
    },
    { deep: true },
  )
}

export async function save() {
  if (!loaded) return
  clearTimeout(timer)
  const desktop = useDesktop()
  const prefs = usePrefs()
  const state: SavedState = { version: 1, prefs: prefs.p, desktop: desktop.serialize() }
  try {
    await api.put('/desktop/state', state)
  } catch {
    /* 下次变化时重试 */
  }
}

export function stopPersist() {
  stop?.()
  stop = null
  clearTimeout(timer)
  loaded = false
}

// 关闭页面前尽量保存一次
window.addEventListener('beforeunload', () => {
  if (!loaded || timer === undefined) return
  const desktop = useDesktop()
  const prefs = usePrefs()
  const body = JSON.stringify({ version: 1, prefs: prefs.p, desktop: desktop.serialize() })
  fetch('/api/v1/desktop/state', {
    method: 'PUT',
    body,
    keepalive: true,
    headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'sshdesktop' },
  })
})
