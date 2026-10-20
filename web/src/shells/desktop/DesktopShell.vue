<script setup lang="ts">
// 桌面外观（Windows 11 风格）：壁纸、桌面图标、窗口、任务栏、开始菜单、通知中心、小组件。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useDesktop, type Snap } from '@/stores/desktop'
import { usePrefs } from '@/stores/prefs'
import { useSession } from '@/stores/session'
import { wallpaperCSS } from '@/theme/wallpapers'
import { useShellBoot } from '@/shells/common'
import AppWindow from './AppWindow.vue'
import Taskbar from './Taskbar.vue'
import StartMenu from './StartMenu.vue'
import NotificationCenter from './NotificationCenter.vue'
import WidgetsPanel from './WidgetsPanel.vue'
import Toasts from './Toasts.vue'
import DesktopIcons from './DesktopIcons.vue'
import FileDialogs from '@/apps/explorer/FileDialogs.vue'

useShellBoot()
const desktop = useDesktop()
const prefs = usePrefs()
const session = useSession()

type Panel = 'start' | 'search' | 'notify' | 'widgets' | null
const panel = ref<Panel>(null)
const snapPreview = ref<Snap | 'max' | null>(null)

const bg = computed(() => wallpaperCSS(prefs.p.wallpaper, prefs.dark))

const previewRect = computed(() => {
  const s = snapPreview.value
  if (!s) return null
  const fake = { maximized: s === 'max', snap: s === 'max' ? null : s, x: 0, y: 0, w: 0, h: 0 } as never
  return desktop.snapRect(fake)
})

function toggle(p: Exclude<Panel, null>) {
  panel.value = panel.value === p || (p === 'search' && panel.value === 'start') ? null : p
}

function openFromWidget(id: string) {
  panel.value = null
  desktop.open(id)
}

// 浏览器会占用 Win、Alt+Tab、Alt+F4，因此使用 Alt 组合键
function onKey(e: KeyboardEvent) {
  if (e.key === 'Escape' && panel.value) {
    panel.value = null
    return
  }
  if (!e.altKey || e.ctrlKey || e.metaKey) return
  const k = e.key.toLowerCase()
  if (k === 'e' && session.has('files:view')) desktop.open('explorer')
  else if (k === 't' && session.has('terminal:use')) desktop.open('terminal')
  else if (k === 'q') desktop.cycle()
  else if (k === 'w' && desktop.activeId) desktop.close(desktop.activeId)
  else if (k === 'd') desktop.toggleDesktop()
  else if (k === 's') toggle('start')
  else return
  e.preventDefault()
  e.stopPropagation()
}

function onResize() {
  desktop.onResize()
}

onMounted(() => {
  window.addEventListener('keydown', onKey, true)
  window.addEventListener('resize', onResize)
  desktop.onResize()
})
onUnmounted(() => {
  window.removeEventListener('keydown', onKey, true)
  window.removeEventListener('resize', onResize)
})
</script>

<template>
  <div class="shell" :style="{ backgroundImage: bg }" @click="panel = null" @contextmenu.prevent>
    <DesktopIcons />
    <div class="windows">
      <AppWindow v-for="w in desktop.windows" :key="w.id" :win="w" @snap-preview="snapPreview = $event" />
      <div
        v-if="previewRect"
        class="snap-preview"
        :style="{ left: previewRect.x + 8 + 'px', top: previewRect.y + 8 + 'px', width: previewRect.w - 16 + 'px', height: previewRect.h - 16 + 'px' }"
      />
    </div>
    <Taskbar
      :start-open="panel === 'start' || panel === 'search'"
      :notify-open="panel === 'notify'"
      :widgets-open="panel === 'widgets'"
      @toggle="toggle"
      @click.stop
    />
    <StartMenu v-if="panel === 'start' || panel === 'search'" :search-mode="panel === 'search'" @close="panel = null" />
    <NotificationCenter v-if="panel === 'notify'" />
    <WidgetsPanel v-if="panel === 'widgets'" @open="openFromWidget" />
    <Toasts v-if="panel !== 'notify'" />
    <FileDialogs />
  </div>
</template>

<style scoped>
.shell {
  position: fixed;
  inset: 0;
  overflow: hidden;
  background-size: cover;
  background-position: center;
}
.windows {
  position: absolute;
  inset: 0 0 48px 0;
  pointer-events: none;
}
.windows > :deep(.win) {
  pointer-events: auto;
}
.snap-preview {
  position: absolute;
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.25);
  border: 1px solid rgba(255, 255, 255, 0.5);
  backdrop-filter: blur(12px);
  z-index: 99999;
  transition: all 0.12s;
  pointer-events: none;
}
</style>
