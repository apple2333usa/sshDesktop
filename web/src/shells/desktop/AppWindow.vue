<script setup lang="ts">
// 桌面窗口：拖动、八向缩放、最小化 / 最大化 / 关闭、双击标题栏最大化、拖到屏幕边缘贴靠。
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useDesktop, addCloseGuard, type Snap, type WinState } from '@/stores/desktop'
import { appMap } from '@/apps/registry'
import AppContainer from '@/apps/AppContainer.vue'
import SdIcon from '@/components/SdIcon.vue'
import { useNodes } from '@/stores/nodes'

const props = defineProps<{ win: WinState }>()
const emit = defineEmits<{ snapPreview: [Snap | 'max' | null] }>()

const { t } = useI18n()
const desktop = useDesktop()
const nodes = useNodes()
const app = appMap[props.win.appId]
const dragging = ref(false)

const isActive = computed(() => desktop.activeId === props.win.id && !props.win.minimized)
const rect = computed(() => desktop.snapRect(props.win))
const fullTitle = computed(() => {
  const base = t(app.title)
  return props.win.title ? `${props.win.title} - ${base}` : base
})
const style = computed(() => ({
  transform: `translate(${rect.value.x}px, ${rect.value.y}px)`,
  width: `${rect.value.w}px`,
  height: `${rect.value.h}px`,
  zIndex: props.win.z,
}))

const EDGE = 8

function detectSnap(x: number, y: number): Snap | 'max' | null {
  const vw = desktop.viewport.w
  const vh = desktop.viewport.h
  const corner = 40
  if (y <= EDGE) {
    if (x <= corner) return 'tl'
    if (x >= vw - corner) return 'tr'
    return 'max'
  }
  if (x <= EDGE) {
    if (y >= vh - corner) return 'bl'
    if (y <= corner) return 'tl'
    return 'left'
  }
  if (x >= vw - EDGE) {
    if (y >= vh - corner) return 'br'
    if (y <= corner) return 'tr'
    return 'right'
  }
  return null
}

function onTitleDown(e: PointerEvent) {
  if (e.button !== 0 || (e.target as HTMLElement).closest('.btns')) return
  desktop.focus(props.win.id)
  const w = props.win
  const startX = e.clientX
  const startY = e.clientY
  let origin = { x: rect.value.x, y: rect.value.y }
  let moved = false
  let snap: Snap | 'max' | null = null
  const target = e.currentTarget as HTMLElement
  target.setPointerCapture(e.pointerId)

  const move = (ev: PointerEvent) => {
    const dx = ev.clientX - startX
    const dy = ev.clientY - startY
    if (!moved) {
      if (Math.abs(dx) + Math.abs(dy) < 4) return
      moved = true
      dragging.value = true
      const r = desktop.unsnapForDrag(w.id, startX)
      if (r) origin = { x: r.x, y: r.y }
    }
    w.x = Math.round(origin.x + dx)
    w.y = Math.max(0, Math.round(origin.y + dy))
    snap = detectSnap(ev.clientX, ev.clientY)
    emit('snapPreview', snap)
  }
  const up = () => {
    target.removeEventListener('pointermove', move)
    target.removeEventListener('pointerup', up)
    target.removeEventListener('pointercancel', up)
    dragging.value = false
    emit('snapPreview', null)
    if (moved) {
      if (snap) desktop.snapTo(w.id, snap)
      else desktop.clamp(w)
    }
  }
  target.addEventListener('pointermove', move)
  target.addEventListener('pointerup', up)
  target.addEventListener('pointercancel', up)
}

function onResizeDown(e: PointerEvent, dir: string) {
  if (e.button !== 0) return
  e.stopPropagation()
  desktop.focus(props.win.id)
  const w = props.win
  const r0 = { ...rect.value }
  if (w.maximized || w.snap) {
    w.maximized = false
    w.snap = null
    Object.assign(w, r0)
  }
  const minW = app.window.minWidth ?? 320
  const minH = app.window.minHeight ?? 200
  const sx = e.clientX
  const sy = e.clientY
  const target = e.currentTarget as HTMLElement
  target.setPointerCapture(e.pointerId)
  dragging.value = true
  const move = (ev: PointerEvent) => {
    const dx = ev.clientX - sx
    const dy = ev.clientY - sy
    if (dir.includes('e')) w.w = Math.max(minW, r0.w + dx)
    if (dir.includes('s')) w.h = Math.max(minH, Math.min(r0.h + dy, desktop.viewport.h - r0.y))
    if (dir.includes('w')) {
      const nw = Math.max(minW, r0.w - dx)
      w.x = r0.x + (r0.w - nw)
      w.w = nw
    }
    if (dir.includes('n')) {
      const nh = Math.max(minH, r0.h - dy)
      const ny = r0.y + (r0.h - nh)
      if (ny >= 0) {
        w.y = ny
        w.h = nh
      }
    }
  }
  const up = () => {
    target.removeEventListener('pointermove', move)
    target.removeEventListener('pointerup', up)
    dragging.value = false
  }
  target.addEventListener('pointermove', move)
  target.addEventListener('pointerup', up)
}

function onTitle(title: string) {
  props.win.title = title
}
</script>

<template>
  <div
    class="win"
    :class="{ active: isActive, max: win.maximized, snapped: !!win.snap, min: win.minimized, dragging }"
    :style="style"
    role="dialog"
    :aria-label="fullTitle"
    :data-app="win.appId"
    :data-testid="'window-' + win.appId"
    @pointerdown="desktop.focus(win.id)"
  >
    <div class="titlebar" @pointerdown="onTitleDown" @dblclick="desktop.toggleMaximize(win.id)">
      <SdIcon :name="app.icon" :size="16" />
      <span class="title sd-ellipsis">{{ fullTitle }}</span>
      <span
        v-if="win.props.node && win.props.node !== 'local'"
        class="node-tag"
        :class="{ off: !nodes.online(win.props.node) }"
        :style="{ background: nodes.color(win.props.node) }"
        :title="nodes.label(win.props.node)"
        data-testid="win-node-tag"
      >{{ nodes.name(win.props.node) }}</span>
      <div class="btns">
        <button class="btn" :title="t('window.minimize')" data-testid="win-min" @click="desktop.minimize(win.id)">
          <svg viewBox="0 0 10 10"><path d="M0 5h10" stroke="currentColor" /></svg>
        </button>
        <button
          class="btn"
          :title="win.maximized ? t('window.restore') : t('window.maximize')"
          data-testid="win-max"
          @click="desktop.toggleMaximize(win.id)"
        >
          <svg v-if="!win.maximized" viewBox="0 0 10 10"><rect x=".5" y=".5" width="9" height="9" rx="1" fill="none" stroke="currentColor" /></svg>
          <svg v-else viewBox="0 0 10 10">
            <rect x=".5" y="2.5" width="7" height="7" rx="1" fill="none" stroke="currentColor" />
            <path d="M2.5 2.5V1.5a1 1 0 0 1 1-1h5a1 1 0 0 1 1 1v5a1 1 0 0 1-1 1h-1" fill="none" stroke="currentColor" />
          </svg>
        </button>
        <button class="btn close" :title="t('window.close')" data-testid="win-close" @click="desktop.close(win.id)">
          <svg viewBox="0 0 10 10"><path d="M0 0l10 10M10 0L0 10" stroke="currentColor" /></svg>
        </button>
      </div>
    </div>
    <div class="content">
      <AppContainer
        :app-id="win.appId"
        :app-props="win.props"
        mode="desktop"
        :window-id="win.id"
        :active="isActive"
        :visible="!win.minimized"
        @title="onTitle"
        @close="desktop.close(win.id)"
        @open="(id, p) => desktop.open(id, p)"
        @guard="(fn) => addCloseGuard(win.id, fn)"
      />
    </div>
    <template v-if="!win.maximized">
      <div v-for="d in ['n', 's', 'e', 'w', 'ne', 'nw', 'se', 'sw']" :key="d" :class="['rz', d]" @pointerdown="onResizeDown($event, d)" />
    </template>
  </div>
</template>

<style scoped>
.win {
  position: absolute;
  left: 0;
  top: 0;
  display: flex;
  flex-direction: column;
  background: var(--sd-surface);
  border: 1px solid var(--sd-border-strong);
  border-radius: var(--sd-radius);
  box-shadow: var(--sd-shadow-inactive);
  overflow: hidden;
  transition: box-shadow 0.15s, opacity 0.15s;
  contain: layout paint;
}
.win.active {
  box-shadow: var(--sd-shadow);
}
.win.max,
.win.snapped {
  border-radius: 0;
}
.win.max {
  border: none;
}
.win.min {
  display: none;
}
.win.dragging .content {
  pointer-events: none;
}
.titlebar {
  height: 34px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  padding-left: 12px;
  background: var(--sd-mica);
  user-select: none;
  touch-action: none;
}
.win:not(.active) .titlebar .title {
  color: var(--sd-text-2);
}
.title {
  flex: 1;
  font-size: 12px;
}
.node-tag {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 8px;
  background: #8764b8;
  color: #fff;
  max-width: 160px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.node-tag.off {
  opacity: 0.55;
  text-decoration: line-through;
}
.btns {
  display: flex;
  align-self: stretch;
}
.btn {
  width: 46px;
  border: none;
  background: transparent;
  color: var(--sd-text);
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: default;
}
.btn svg {
  width: 10px;
  height: 10px;
}
.btn:hover {
  background: var(--sd-hover);
}
.btn.close:hover {
  background: #c42b1c;
  color: #fff;
}
.content {
  flex: 1;
  min-height: 0;
  position: relative;
  background: var(--sd-surface);
}
.rz {
  position: absolute;
  z-index: 2;
}
.rz.n,
.rz.s {
  left: 8px;
  right: 8px;
  height: 6px;
  cursor: ns-resize;
}
.rz.n {
  top: -3px;
}
.rz.s {
  bottom: -3px;
}
.rz.e,
.rz.w {
  top: 8px;
  bottom: 8px;
  width: 6px;
  cursor: ew-resize;
}
.rz.e {
  right: -3px;
}
.rz.w {
  left: -3px;
}
.rz.ne,
.rz.nw,
.rz.se,
.rz.sw {
  width: 12px;
  height: 12px;
}
.rz.ne {
  top: -3px;
  right: -3px;
  cursor: nesw-resize;
}
.rz.sw {
  bottom: -3px;
  left: -3px;
  cursor: nesw-resize;
}
.rz.nw {
  top: -3px;
  left: -3px;
  cursor: nwse-resize;
}
.rz.se {
  bottom: -3px;
  right: -3px;
  cursor: nwse-resize;
}
</style>
