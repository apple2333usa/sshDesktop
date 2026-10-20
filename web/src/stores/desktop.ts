// 桌面窗口管理器状态：窗口位置、层级、最小化 / 最大化 / 贴靠，以及桌面图标排列。
import { defineStore } from 'pinia'
import { computed, reactive, ref } from 'vue'
import { appMap } from '@/apps/registry'

export type Snap = 'left' | 'right' | 'tl' | 'tr' | 'bl' | 'br' | null

export interface Rect {
  x: number
  y: number
  w: number
  h: number
}

export interface WinState extends Rect {
  id: string
  appId: string
  title: string
  props: Record<string, any>
  z: number
  minimized: boolean
  maximized: boolean
  snap: Snap
  restore: Rect | null
}

/** 保存到服务器的窗口状态：位置按视口比例保存，换分辨率时按比例缩放。 */
interface SavedWin {
  appId: string
  title: string
  props: Record<string, any>
  rx: number
  ry: number
  rw: number
  rh: number
  z: number
  minimized: boolean
  maximized: boolean
  snap: Snap
}

export const TASKBAR_H = 48

const closeGuards = new Map<string, (() => boolean | Promise<boolean>)[]>()

export function addCloseGuard(winId: string, fn: () => boolean | Promise<boolean>) {
  const arr = closeGuards.get(winId) ?? []
  arr.push(fn)
  closeGuards.set(winId, arr)
}

let seq = 0
const newId = () => `w${Date.now().toString(36)}${(++seq).toString(36)}`

export const useDesktop = defineStore('desktop', () => {
  const windows = ref<WinState[]>([])
  const topZ = ref(10)
  const activeId = ref<string | null>(null)
  const icons = ref<string[]>([])
  const viewport = reactive({ w: window.innerWidth, h: window.innerHeight - TASKBAR_H })
  const desktopShown = ref<string[] | null>(null)

  const active = computed(() => windows.value.find((w) => w.id === activeId.value && !w.minimized) ?? null)
  const ordered = computed(() => [...windows.value].sort((a, b) => a.z - b.z))

  function onResize() {
    viewport.w = window.innerWidth
    viewport.h = window.innerHeight - TASKBAR_H
    for (const w of windows.value) clamp(w)
  }

  function clamp(w: WinState) {
    const app = appMap[w.appId]
    const minW = app?.window.minWidth ?? 320
    const minH = app?.window.minHeight ?? 200
    w.w = Math.min(Math.max(w.w, minW), viewport.w)
    w.h = Math.min(Math.max(w.h, minH), viewport.h)
    w.x = Math.min(Math.max(w.x, -w.w + 120), viewport.w - 120)
    w.y = Math.min(Math.max(w.y, 0), viewport.h - 40)
  }

  function focus(id: string) {
    const w = windows.value.find((x) => x.id === id)
    if (!w) return
    w.minimized = false
    if (w.z !== topZ.value) w.z = ++topZ.value
    activeId.value = id
    desktopShown.value = null
  }

  function sameTarget(w: WinState, appId: string, props: Record<string, any>) {
    if (w.appId !== appId) return false
    if (appMap[appId]?.window.singleton) return true
    // 编辑器、查看器：同一个文件只开一个窗口
    return !!props.path && (appId === 'editor' || appId === 'viewer') && w.props.path === props.path && w.props.node === props.node
  }

  function open(appId: string, props: Record<string, any> = {}): string | null {
    const app = appMap[appId]
    if (!app) return null
    const existing = windows.value.find((w) => sameTarget(w, appId, props))
    if (existing) {
      if (Object.keys(props).length) Object.assign(existing.props, props)
      focus(existing.id)
      return existing.id
    }
    const w = Math.min(app.window.width, viewport.w - 40)
    const h = Math.min(app.window.height, viewport.h - 40)
    const n = windows.value.filter((x) => !x.minimized).length
    const offset = (n % 8) * 28
    const win: WinState = {
      id: newId(),
      appId,
      title: '',
      props: { ...props },
      x: Math.max(0, Math.round((viewport.w - w) / 2 - 80 + offset)),
      y: Math.max(0, Math.round((viewport.h - h) / 2 - 60 + offset)),
      w,
      h,
      z: ++topZ.value,
      minimized: false,
      maximized: viewport.w < 900,
      snap: null,
      restore: null,
    }
    windows.value.push(win)
    activeId.value = win.id
    desktopShown.value = null
    return win.id
  }

  async function close(id: string, force = false) {
    if (!force) {
      for (const fn of closeGuards.get(id) ?? []) {
        if (!(await fn())) return false
      }
    }
    closeGuards.delete(id)
    windows.value = windows.value.filter((w) => w.id !== id)
    if (activeId.value === id) {
      const next = [...windows.value].filter((w) => !w.minimized).sort((a, b) => b.z - a.z)[0]
      activeId.value = next?.id ?? null
    }
    return true
  }

  function minimize(id: string) {
    const w = windows.value.find((x) => x.id === id)
    if (!w) return
    w.minimized = true
    if (activeId.value === id) {
      const next = [...windows.value].filter((x) => !x.minimized).sort((a, b) => b.z - a.z)[0]
      activeId.value = next?.id ?? null
    }
  }

  function toggleMaximize(id: string) {
    const w = windows.value.find((x) => x.id === id)
    if (!w) return
    if (w.maximized) {
      w.maximized = false
    } else {
      if (w.snap && w.restore) Object.assign(w, w.restore)
      w.snap = null
      w.maximized = true
    }
    focus(id)
  }

  /** 贴靠到半屏或四分之一屏；snap 为 null 时还原。 */
  function snapTo(id: string, snap: Snap | 'max') {
    const w = windows.value.find((x) => x.id === id)
    if (!w) return
    if (snap === 'max') {
      w.maximized = true
      return
    }
    if (snap && !w.snap) w.restore = { x: w.x, y: w.y, w: w.w, h: w.h }
    w.snap = snap
    w.maximized = false
  }

  /** 拖动已贴靠或最大化的窗口时恢复原尺寸，并让鼠标保持在标题栏相对位置。 */
  function unsnapForDrag(id: string, pointerX: number): Rect | null {
    const w = windows.value.find((x) => x.id === id)
    if (!w || (!w.snap && !w.maximized)) return null
    const cur = snapRect(w)
    const r = w.restore ?? { x: w.x, y: w.y, w: w.w, h: w.h }
    const ratio = (pointerX - cur.x) / cur.w
    w.snap = null
    w.maximized = false
    w.w = r.w
    w.h = r.h
    w.x = Math.round(pointerX - r.w * ratio)
    w.y = 0
    return { x: w.x, y: w.y, w: w.w, h: w.h }
  }

  /** 计算窗口当前实际显示的矩形。 */
  function snapRect(w: WinState): Rect {
    const vw = viewport.w
    const vh = viewport.h
    if (w.maximized) return { x: 0, y: 0, w: vw, h: vh }
    const hw = Math.round(vw / 2)
    const hh = Math.round(vh / 2)
    switch (w.snap) {
      case 'left':
        return { x: 0, y: 0, w: hw, h: vh }
      case 'right':
        return { x: hw, y: 0, w: vw - hw, h: vh }
      case 'tl':
        return { x: 0, y: 0, w: hw, h: hh }
      case 'tr':
        return { x: hw, y: 0, w: vw - hw, h: hh }
      case 'bl':
        return { x: 0, y: hh, w: hw, h: vh - hh }
      case 'br':
        return { x: hw, y: hh, w: vw - hw, h: vh - hh }
    }
    return { x: w.x, y: w.y, w: w.w, h: w.h }
  }

  /** 显示桌面：再按一次恢复之前的窗口。 */
  function toggleDesktop() {
    if (desktopShown.value) {
      for (const id of desktopShown.value) {
        const w = windows.value.find((x) => x.id === id)
        if (w) w.minimized = false
      }
      desktopShown.value = null
      const top = [...windows.value].filter((w) => !w.minimized).sort((a, b) => b.z - a.z)[0]
      activeId.value = top?.id ?? null
      return
    }
    const visible = windows.value.filter((w) => !w.minimized).map((w) => w.id)
    windows.value.forEach((w) => (w.minimized = true))
    desktopShown.value = visible
    activeId.value = null
  }

  /** Alt+Q：在窗口间切换（按最近使用顺序）。 */
  function cycle() {
    const list = [...windows.value].sort((a, b) => b.z - a.z)
    if (list.length < 2 && list[0]?.minimized === false) return
    const target = list.length > 1 && list[0].id === activeId.value ? list[list.length - 1] : list[0]
    if (target) focus(target.id)
  }

  function serialize() {
    const vw = viewport.w || 1
    const vh = viewport.h || 1
    return {
      windows: windows.value.map<SavedWin>((w) => ({
        appId: w.appId,
        title: w.title,
        props: w.props,
        rx: w.x / vw,
        ry: w.y / vh,
        rw: w.w / vw,
        rh: w.h / vh,
        z: w.z,
        minimized: w.minimized,
        maximized: w.maximized,
        snap: w.snap,
      })),
      icons: icons.value,
    }
  }

  // 恢复保存的窗口。读取是异步的，期间用户已经打开的窗口保留在最上层（同一目标以已打开的为准）。
  function restore(saved: { windows?: SavedWin[]; icons?: string[] } | undefined) {
    const opened = windows.value
    windows.value = []
    if (!saved) {
      windows.value = opened
      return
    }
    icons.value = saved.icons ?? []
    for (const s of saved.windows ?? []) {
      if (!appMap[s.appId]) continue
      if (opened.some((w) => sameTarget(w, s.appId, s.props ?? {}))) continue
      const w: WinState = {
        id: newId(),
        appId: s.appId,
        title: s.title ?? '',
        props: s.props ?? {},
        x: Math.round(s.rx * viewport.w),
        y: Math.round(s.ry * viewport.h),
        w: Math.round(s.rw * viewport.w),
        h: Math.round(s.rh * viewport.h),
        z: s.z,
        minimized: s.minimized,
        maximized: s.maximized,
        snap: s.snap ?? null,
        restore: null,
      }
      clamp(w)
      windows.value.push(w)
      topZ.value = Math.max(topZ.value, w.z)
    }
    for (const w of opened) {
      w.z = ++topZ.value
      windows.value.push(w)
    }
    const top = [...windows.value].filter((w) => !w.minimized).sort((a, b) => b.z - a.z)[0]
    activeId.value = top?.id ?? null
  }

  function reset() {
    windows.value = []
    activeId.value = null
    closeGuards.clear()
  }

  return {
    windows, ordered, activeId, active, icons, viewport, topZ,
    onResize, clamp, focus, open, close, minimize, toggleMaximize, snapTo, unsnapForDrag, snapRect,
    toggleDesktop, cycle, serialize, restore, reset,
  }
})
