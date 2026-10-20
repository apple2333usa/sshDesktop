<script setup lang="ts">
// 任务栏：居中的开始按钮、搜索、固定的应用和运行中的窗口；右侧托盘为连接状态、CPU / 网速、通知中心和时钟。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NTooltip } from 'naive-ui'
import { useDesktop } from '@/stores/desktop'
import { usePrefs } from '@/stores/prefs'
import { useNotify } from '@/stores/notify'
import { useSession } from '@/stores/session'
import { realtime } from '@/api/realtime'
import { appMap } from '@/apps/registry'
import { formatRate } from '@/i18n'
import type { Sample } from '@/api/types'
import SdIcon from '@/components/SdIcon.vue'

const props = defineProps<{ startOpen: boolean; notifyOpen: boolean; widgetsOpen: boolean }>()
const emit = defineEmits<{ toggle: ['start' | 'search' | 'notify' | 'widgets'] }>()

const { t } = useI18n()
const desktop = useDesktop()
const prefs = usePrefs()
const notify = useNotify()
const session = useSession()

const now = ref(new Date())
const sample = ref<Sample | null>(null)
let timer: number | undefined
let unsub: (() => void) | null = null

onMounted(() => {
  timer = window.setInterval(() => (now.value = new Date()), 1000)
  if (session.has('monitor:view')) unsub = realtime.subscribe('metrics:local', (s: Sample) => (sample.value = s))
})
onUnmounted(() => {
  clearInterval(timer)
  unsub?.()
})

interface Item {
  appId: string
  pinned: boolean
  wins: string[]
}

// 固定的应用在前，运行中但未固定的追加在后
const items = computed<Item[]>(() => {
  const list: Item[] = prefs.p.pinned.filter((id) => appMap[id] && session.has(appMap[id].permission)).map((id) => ({ appId: id, pinned: true, wins: [] }))
  for (const w of desktop.windows) {
    let it = list.find((x) => x.appId === w.appId)
    if (!it) {
      it = { appId: w.appId, pinned: false, wins: [] }
      list.push(it)
    }
    it.wins.push(w.id)
  }
  return list
})

function isActive(it: Item) {
  return it.wins.includes(desktop.activeId ?? '')
}

function click(it: Item) {
  if (it.wins.length === 0) {
    desktop.open(it.appId)
    return
  }
  if (it.wins.length === 1) {
    const id = it.wins[0]
    if (desktop.activeId === id) desktop.minimize(id)
    else desktop.focus(id)
    return
  }
  // 多个窗口：依次切换
  const sorted = it.wins
    .map((id) => desktop.windows.find((w) => w.id === id)!)
    .sort((a, b) => b.z - a.z)
  const idx = sorted.findIndex((w) => w.id === desktop.activeId)
  desktop.focus(sorted[(idx + 1) % sorted.length].id)
}

function winTitle(id: string) {
  const w = desktop.windows.find((x) => x.id === id)
  if (!w) return ''
  const base = t(appMap[w.appId].title)
  return w.title ? `${w.title} - ${base}` : base
}

const time = computed(() => now.value.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' }))
const date = computed(() => now.value.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' }).replace(/\//g, '/'))
const connected = realtime.connected
</script>

<template>
  <div class="taskbar" data-testid="taskbar">
    <button class="tb-btn widgets" :class="{ on: props.widgetsOpen }" :title="t('taskbar.widgets')" data-testid="tb-widgets" @click="emit('toggle', 'widgets')">
      <svg viewBox="0 0 24 24"><rect x="3" y="3" width="8" height="10" rx="2" fill="#3a96dd" /><rect x="13" y="3" width="8" height="6" rx="2" fill="#2ec8a0" /><rect x="13" y="11" width="8" height="10" rx="2" fill="#ffb900" /><rect x="3" y="15" width="8" height="6" rx="2" fill="#8764b8" /></svg>
      <span v-if="sample" class="wtext">CPU {{ sample.cpu.toFixed(0) }}%</span>
    </button>

    <div class="center">
      <button class="tb-btn start" :class="{ on: props.startOpen }" :title="t('taskbar.start')" data-testid="tb-start" @click="emit('toggle', 'start')">
        <svg viewBox="0 0 24 24"><rect x="2" y="2" width="9.5" height="9.5" rx="1" fill="#0078d4" /><rect x="12.5" y="2" width="9.5" height="9.5" rx="1" fill="#0078d4" /><rect x="2" y="12.5" width="9.5" height="9.5" rx="1" fill="#0078d4" /><rect x="12.5" y="12.5" width="9.5" height="9.5" rx="1" fill="#0078d4" /></svg>
      </button>
      <button class="tb-btn" :title="t('taskbar.search')" data-testid="tb-search" @click="emit('toggle', 'search')">
        <svg viewBox="0 0 24 24"><circle cx="10" cy="10" r="6.5" fill="none" stroke="currentColor" stroke-width="2" /><path d="M15 15l6 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" /></svg>
      </button>
      <NTooltip v-for="it in items" :key="it.appId" placement="top" :delay="400">
        <template #trigger>
          <button
            class="tb-btn app"
            :class="{ running: it.wins.length > 0, active: isActive(it) }"
            :data-testid="'tb-app-' + it.appId"
            @click="click(it)"
          >
            <SdIcon :name="appMap[it.appId].icon" :size="24" />
            <span class="dot" />
          </button>
        </template>
        <div v-if="it.wins.length === 0">{{ t(appMap[it.appId].title) }}</div>
        <div v-for="id in it.wins" v-else :key="id" class="preview" @click="desktop.focus(id)">{{ winTitle(id) }}</div>
      </NTooltip>
    </div>

    <div class="tray">
      <span v-if="sample" class="net sd-mono" :title="t('taskbar.netSpeed')">
        <span>↑ {{ formatRate(sample.netTxBps) }}</span>
        <span>↓ {{ formatRate(sample.netRxBps) }}</span>
      </span>
      <span class="conn" :class="{ ok: connected }" :title="connected ? t('taskbar.connected') : t('taskbar.disconnected')">
        <svg viewBox="0 0 24 24"><path d="M4 18h3v3H4zM10 13h3v8h-3zM16 7h3v14h-3z" fill="currentColor" /></svg>
      </span>
      <button class="tb-btn clock" :class="{ on: props.notifyOpen }" data-testid="tb-notify" @click="emit('toggle', 'notify')">
        <div class="ct">
          <div>{{ time }}</div>
          <div>{{ date }}</div>
        </div>
        <span v-if="notify.unread" class="badge">{{ notify.unread > 9 ? '9+' : notify.unread }}</span>
      </button>
      <div class="show-desktop" :title="t('taskbar.showDesktop')" @click="desktop.toggleDesktop()" />
    </div>
  </div>
</template>

<style scoped>
.taskbar {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  height: 48px;
  display: flex;
  align-items: center;
  background: var(--sd-taskbar);
  backdrop-filter: blur(30px) saturate(150%);
  border-top: 1px solid var(--sd-border);
  z-index: 100000;
  user-select: none;
}
.center {
  position: absolute;
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  gap: 4px;
  align-items: center;
}
.tb-btn {
  height: 40px;
  min-width: 40px;
  padding: 0 8px;
  border: none;
  border-radius: 4px;
  background: transparent;
  color: var(--sd-text);
  display: flex;
  align-items: center;
  justify-content: center;
  position: relative;
  gap: 6px;
}
.tb-btn svg {
  width: 22px;
  height: 22px;
}
.tb-btn:hover,
.tb-btn.on {
  background: var(--sd-hover);
}
.tb-btn.active {
  background: var(--sd-active);
}
.app .dot {
  position: absolute;
  bottom: 2px;
  left: 50%;
  transform: translateX(-50%);
  width: 6px;
  height: 3px;
  border-radius: 2px;
  background: var(--sd-text-2);
  opacity: 0;
  transition: all 0.15s;
}
.app.running .dot {
  opacity: 1;
}
.app.active .dot {
  width: 16px;
  background: var(--sd-accent);
}
.widgets {
  margin-left: 8px;
}
.wtext {
  font-size: 12px;
}
.tray {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 4px;
  height: 100%;
}
.net {
  display: flex;
  flex-direction: column;
  font-size: 10px;
  line-height: 1.25;
  color: var(--sd-text-2);
  padding: 0 6px;
  min-width: 92px;
}
.conn {
  color: var(--sd-danger);
  display: flex;
  padding: 0 4px;
}
.conn.ok {
  color: var(--sd-text);
}
.conn svg {
  width: 16px;
  height: 16px;
}
.clock .ct {
  font-size: 12px;
  text-align: right;
  line-height: 1.3;
}
.badge {
  position: absolute;
  top: 2px;
  right: 0;
  min-width: 16px;
  height: 16px;
  border-radius: 8px;
  background: var(--sd-accent);
  color: #fff;
  font-size: 10px;
  line-height: 16px;
  padding: 0 4px;
}
:root[data-theme='dark'] .badge {
  color: #000;
}
.show-desktop {
  width: 8px;
  height: 100%;
  border-left: 1px solid var(--sd-border);
  margin-left: 4px;
}
.show-desktop:hover {
  background: var(--sd-hover);
}
.preview {
  padding: 4px 0;
  cursor: pointer;
}
</style>
