<script setup lang="ts">
// 终端：多标签、分屏（每个标签最多 4 个面板）、同步输入、常用命令片段。
// 会话在服务端独立存在，刷新页面后按保存的会话 ID 自动接回。
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDropdown, NTooltip, useMessage, type DropdownOption } from 'naive-ui'
import { api } from '@/api/http'
import type { TerminalSession } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { usePrefs } from '@/stores/prefs'
import { errorText } from '@/i18n'
import TermPane from './TermPane.vue'

interface Tab {
  id: number
  panes: string[]
  layout: 'h' | 'v'
  focus: number
}

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const prefs = usePrefs()
const node = (host.props.node as string) || 'local'
const base = `/nodes/${node}/terminals`

const tabs = ref<Tab[]>([])
const activeTab = ref(0)
const titles = reactive<Record<string, string>>({})
const sync = ref(false)
const others = ref<TerminalSession[]>([])
const paneRefs = new Map<string, InstanceType<typeof TermPane>>()
let seq = 0

const current = computed(() => tabs.value[activeTab.value])

function persist() {
  host.setState({
    node,
    tabs: tabs.value.map((x) => ({ panes: x.panes, layout: x.layout, focus: x.focus })),
    activeTab: activeTab.value,
    cwd: undefined,
  })
}
watch([tabs, activeTab], persist, { deep: true })

async function createSession(dir?: string): Promise<string | null> {
  try {
    const s = await api.post<TerminalSession>(base, { cols: 120, rows: 32, dir: dir ?? '' })
    return s.id
  } catch (e) {
    message.error(errorText(e))
    return null
  }
}

async function newTab(dir?: string, sid?: string) {
  const id = sid ?? (await createSession(dir))
  if (!id) return
  tabs.value.push({ id: ++seq, panes: [id], layout: 'h', focus: 0 })
  activeTab.value = tabs.value.length - 1
}

async function split(layout: 'h' | 'v') {
  const tab = current.value
  if (!tab) return newTab()
  if (tab.panes.length >= 4) {
    message.info(t('terminal.maxPanes'))
    return
  }
  const id = await createSession()
  if (!id) return
  tab.layout = layout
  tab.panes.push(id)
  tab.focus = tab.panes.length - 1
}

function removePane(sid: string) {
  for (let i = 0; i < tabs.value.length; i++) {
    const tab = tabs.value[i]
    const idx = tab.panes.indexOf(sid)
    if (idx < 0) continue
    tab.panes.splice(idx, 1)
    tab.focus = Math.min(tab.focus, tab.panes.length - 1)
    if (!tab.panes.length) {
      tabs.value.splice(i, 1)
      if (activeTab.value >= tabs.value.length) activeTab.value = Math.max(0, tabs.value.length - 1)
    }
    break
  }
  delete titles[sid]
  paneRefs.delete(sid)
  if (!tabs.value.length) host.close()
}

async function killSession(sid: string) {
  try {
    await api.del(`${base}/${sid}`)
  } catch {
    /* 会话可能已结束 */
  }
}

async function closePane(sid: string) {
  await killSession(sid)
  removePane(sid)
}

async function closeTab(i: number) {
  const tab = tabs.value[i]
  if (!tab) return
  await Promise.all(tab.panes.map(killSession))
  ;[...tab.panes].forEach(removePane)
}

// 关闭窗口时结束本窗口的所有会话
host.onBeforeClose(async () => {
  await Promise.all(tabs.value.flatMap((x) => x.panes).map(killSession))
  return true
})

function onInput(data: string) {
  current.value?.panes.forEach((sid) => paneRefs.get(sid)?.send(data))
}

function setRef(sid: string, el: unknown) {
  if (el) paneRefs.set(sid, el as InstanceType<typeof TermPane>)
}

function focusedSid() {
  const tab = current.value
  return tab ? tab.panes[tab.focus] : undefined
}

function runSnippet(cmd: string) {
  const sids = sync.value ? (current.value?.panes ?? []) : [focusedSid()].filter(Boolean) as string[]
  sids.forEach((sid) => paneRefs.get(sid)?.send(cmd + '\r'))
  nextTick(() => paneRefs.get(focusedSid() ?? '')?.focus())
}

const tabTitle = (tab: Tab, i: number) => titles[tab.panes[tab.focus]] || `${t('apps.terminal')} ${i + 1}`

watch(
  () => (current.value ? tabTitle(current.value, activeTab.value) : ''),
  (v) => host.setTitle(v),
  { immediate: true },
)

/* ---------- 菜单 ---------- */
async function loadOthers() {
  try {
    const list = await api.get<TerminalSession[]>(base)
    const mine = new Set(tabs.value.flatMap((x) => x.panes))
    others.value = list.filter((s) => !mine.has(s.id) && !s.exited)
  } catch {
    others.value = []
  }
}

const addOptions = computed<DropdownOption[]>(() => {
  const opts: DropdownOption[] = [
    { key: 'new', label: t('terminal.newTab') },
    { key: 'splitH', label: t('terminal.splitRight') },
    { key: 'splitV', label: t('terminal.splitDown') },
  ]
  if (others.value.length) {
    opts.push({ type: 'divider', key: 'd' })
    opts.push({
      key: 'attach',
      label: t('terminal.reattach'),
      children: others.value.map((s) => ({
        key: 'attach:' + s.id,
        label: `${s.title || t('apps.terminal')} · ${new Date(s.createdAt).toLocaleTimeString()}${s.attached ? ' · ' + t('terminal.inUse') : ''}`,
      })),
    })
  }
  return opts
})

function onAdd(key: string) {
  if (key === 'new') newTab()
  else if (key === 'splitH') split('h')
  else if (key === 'splitV') split('v')
  else if (key.startsWith('attach:')) newTab(undefined, key.slice(7))
}

const snippetOptions = computed<DropdownOption[]>(() => {
  const list = prefs.p.terminal.snippets
  if (!list.length) return [{ key: '_none', label: t('terminal.noSnippets'), disabled: true }]
  return list.map((s, i) => ({ key: String(i), label: s.name, extra: s.command }))
})

function onSnippet(key: string) {
  const s = prefs.p.terminal.snippets[Number(key)]
  if (s) runSnippet(s.command)
}

function fontSize(delta: number) {
  prefs.p.terminal.fontSize = Math.min(28, Math.max(10, prefs.p.terminal.fontSize + delta))
}

function toggleScheme() {
  prefs.p.terminal.scheme = prefs.p.terminal.scheme === 'dark' ? 'light' : 'dark'
}

function onKey(e: KeyboardEvent) {
  if (!e.ctrlKey || !e.shiftKey) return
  const k = e.key.toLowerCase()
  if (k === 't') newTab()
  else if (k === 'w') {
    const sid = focusedSid()
    if (sid) closePane(sid)
  } else if (k === 'd') split('h')
  else if (k === 'e') split('v')
  else if (k === 'pagedown' || k === 'tab') activeTab.value = (activeTab.value + 1) % Math.max(1, tabs.value.length)
  else if (k === 'pageup') activeTab.value = (activeTab.value - 1 + tabs.value.length) % Math.max(1, tabs.value.length)
  else return
  e.preventDefault()
  e.stopPropagation()
}

/* ---------- 启动：接回已保存的会话，否则新建 ---------- */
onMounted(async () => {
  const saved = host.props.tabs as { panes: string[]; layout: 'h' | 'v'; focus: number }[] | undefined
  if (saved?.length) {
    try {
      const list = await api.get<TerminalSession[]>(base)
      const alive = new Set(list.filter((s) => !s.exited).map((s) => s.id))
      for (const s of list) titles[s.id] = s.title === '终端' ? '' : s.title
      tabs.value = saved
        .map((x) => ({ id: ++seq, panes: x.panes.filter((p) => alive.has(p)), layout: x.layout, focus: x.focus ?? 0 }))
        .filter((x) => x.panes.length)
        .map((x) => ({ ...x, focus: Math.min(x.focus, x.panes.length - 1) }))
      activeTab.value = Math.min((host.props.activeTab as number) ?? 0, Math.max(0, tabs.value.length - 1))
    } catch (e) {
      message.error(errorText(e))
    }
  }
  if (!tabs.value.length) await newTab(host.props.cwd as string | undefined)
  else if (host.props.cwd) await newTab(host.props.cwd as string)
  if (!tabs.value.length) host.close()
})

const gridStyle = computed(() => {
  const tab = current.value
  if (!tab) return {}
  const n = tab.panes.length
  if (n === 4) return { gridTemplateColumns: '1fr 1fr', gridTemplateRows: '1fr 1fr' }
  return tab.layout === 'h' ? { gridTemplateColumns: `repeat(${n}, 1fr)` } : { gridTemplateRows: `repeat(${n}, 1fr)` }
})
</script>

<template>
  <div class="terminal" :class="prefs.p.terminal.scheme" data-testid="terminal" @keydown.capture="onKey">
    <div class="bar">
      <div class="tabs">
        <div
          v-for="(tab, i) in tabs"
          :key="tab.id"
          class="tab"
          :class="{ active: i === activeTab }"
          :data-testid="'term-tab-' + i"
          @click="activeTab = i"
          @mousedown.middle.prevent="closeTab(i)"
        >
          <span class="sd-ellipsis">{{ tabTitle(tab, i) }}</span>
          <span v-if="tab.panes.length > 1" class="count">{{ tab.panes.length }}</span>
          <span class="x" :title="t('common.close')" @click.stop="closeTab(i)">×</span>
        </div>
        <NButton quaternary size="tiny" class="add" :title="t('terminal.newTab') + ' (Ctrl+Shift+T)'" data-testid="term-new" @click="newTab()">＋</NButton>
        <NDropdown trigger="click" :options="addOptions" @select="onAdd" @update:show="(v: boolean) => v && loadOthers()">
          <NButton quaternary size="tiny" class="add">▾</NButton>
        </NDropdown>
      </div>
      <div class="actions">
        <NDropdown trigger="click" :options="snippetOptions" @select="onSnippet">
          <NButton quaternary size="tiny" data-testid="term-snippets">{{ t('terminal.snippets') }} ▾</NButton>
        </NDropdown>
        <NTooltip>
          <template #trigger>
            <NButton quaternary size="tiny" :type="sync ? 'warning' : 'default'" data-testid="term-sync" @click="sync = !sync">
              {{ sync ? t('terminal.syncOn') : t('terminal.sync') }}
            </NButton>
          </template>
          {{ t('terminal.syncHint') }}
        </NTooltip>
        <NButton quaternary size="tiny" :title="t('terminal.splitRight') + ' (Ctrl+Shift+D)'" @click="split('h')">⫴</NButton>
        <NButton quaternary size="tiny" :title="t('terminal.splitDown') + ' (Ctrl+Shift+E)'" @click="split('v')">⊟</NButton>
        <NButton quaternary size="tiny" :title="t('terminal.smaller')" @click="fontSize(-1)">A-</NButton>
        <NButton quaternary size="tiny" :title="t('terminal.larger')" @click="fontSize(1)">A+</NButton>
        <NButton quaternary size="tiny" :title="t('terminal.scheme')" @click="toggleScheme">{{ prefs.p.terminal.scheme === 'dark' ? '☀' : '☾' }}</NButton>
        <NTooltip>
          <template #trigger><span class="root">root</span></template>
          {{ t('terminal.rootHint') }}
        </NTooltip>
      </div>
    </div>
    <template v-for="(tab, i) in tabs" :key="tab.id">
      <div v-show="i === activeTab" class="grid" :style="i === activeTab ? gridStyle : {}">
        <div v-for="(sid, pi) in tab.panes" :key="sid" class="cell" :class="{ focused: tab.panes.length > 1 && pi === tab.focus }">
          <TermPane
            :ref="(el) => setRef(sid, el)"
            :node="node"
            :sid="sid"
            :font-size="prefs.p.terminal.fontSize"
            :scheme="prefs.p.terminal.scheme"
            :sync="sync"
            :active="host.active.value && i === activeTab && pi === tab.focus"
            class="fill"
            @focus="tab.focus = pi"
            @title="(v) => (titles[sid] = v)"
            @input="onInput"
            @exit="removePane(sid)"
            @gone="removePane(sid)"
          />
          <span v-if="tab.panes.length > 1" class="pane-x" :title="t('terminal.closePane')" @click="closePane(sid)">×</span>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.terminal {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.terminal.dark {
  background: #1e1e1e;
  color: #ddd;
}
.terminal.light {
  background: #f3f3f3;
  color: #1b1b1b;
}
.bar {
  display: flex;
  align-items: center;
  height: 34px;
  flex-shrink: 0;
  padding: 0 6px;
  gap: 8px;
}
.dark .bar {
  background: #2b2b2b;
}
.tabs {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: flex-end;
  align-self: stretch;
  gap: 2px;
  overflow-x: auto;
  scrollbar-width: none;
}
.tab {
  display: flex;
  align-items: center;
  gap: 6px;
  height: 28px;
  max-width: 200px;
  min-width: 90px;
  padding: 0 6px 0 10px;
  font-size: 12px;
  border-radius: 6px 6px 0 0;
  cursor: default;
  opacity: 0.75;
}
.tab:hover {
  background: rgba(128, 128, 128, 0.15);
}
.tab.active {
  opacity: 1;
}
.dark .tab.active {
  background: #1e1e1e;
}
.light .tab.active {
  background: #fff;
}
.tab .sd-ellipsis {
  flex: 1;
}
.count {
  font-size: 10px;
  padding: 0 5px;
  border-radius: 8px;
  background: rgba(128, 128, 128, 0.3);
}
.x {
  width: 18px;
  height: 18px;
  line-height: 18px;
  text-align: center;
  border-radius: 4px;
}
.x:hover,
.pane-x:hover {
  background: rgba(128, 128, 128, 0.35);
}
.add {
  align-self: center;
}
.actions {
  display: flex;
  align-items: center;
  gap: 2px;
  flex-shrink: 0;
}
.dark .actions :deep(.n-button) {
  color: #ddd;
}
.root {
  font-size: 11px;
  padding: 0 6px;
  border-radius: 4px;
  color: #fff;
  background: #c42b1c;
  margin-left: 4px;
}
.grid {
  flex: 1;
  min-height: 0;
  display: grid;
  gap: 2px;
}
.dark .grid {
  background: #333;
}
.light .grid {
  background: #ddd;
}
.cell {
  position: relative;
  min-width: 0;
  min-height: 0;
}
.cell.focused :deep(.pane) {
  border-color: var(--sd-accent);
}
.fill {
  position: absolute;
  inset: 0;
}
.pane-x {
  position: absolute;
  top: 4px;
  right: 14px;
  width: 20px;
  height: 20px;
  line-height: 20px;
  text-align: center;
  border-radius: 4px;
  font-size: 13px;
  opacity: 0;
  transition: opacity 0.15s;
  color: #aaa;
}
.cell:hover .pane-x {
  opacity: 1;
}
@media (max-width: 640px) {
  .actions :deep(.n-button):not([data-testid]) {
    display: none;
  }
}
</style>
