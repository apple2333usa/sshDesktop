<script setup lang="ts">
// 桌面图标：系统图标（此电脑、回收站、常用应用）+ 桌面目录中的文件；可拖动排序、框选、右键菜单、拖入上传。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NDropdown, type DropdownOption } from 'naive-ui'
import { filesApi } from '@/api/files'
import type { FileEntry } from '@/api/types'
import { fileKind } from '@/components/icons'
import SdIcon from '@/components/SdIcon.vue'
import { useDesktop } from '@/stores/desktop'
import { usePrefs } from '@/stores/prefs'
import { useSession } from '@/stores/session'
import { useFileClipboard } from '@/stores/clipboard'
import { realtime } from '@/api/realtime'
import { appMap } from '@/apps/registry'
import { useFileOps, collectDropped } from '@/apps/explorer/fileops'
import { useFileOpener } from '@/apps/explorer/opener'

const { t } = useI18n()
const desktop = useDesktop()
const prefs = usePrefs()
const session = useSession()
const clip = useFileClipboard()

const files = ref<FileEntry[]>([])
const selected = ref<Set<string>>(new Set())
const menu = ref<{ x: number; y: number; options: DropdownOption[]; onSelect: (k: string) => void } | null>(null)
const marquee = ref<{ x0: number; y0: number; x1: number; y1: number } | null>(null)
const dropHover = ref<string | null>(null)
const root = ref<HTMLElement | null>(null)

const dir = computed(() => prefs.p.desktopDir || '/root/Desktop')
const canFiles = computed(() => session.has('files:view'))
const canManage = computed(() => session.has('files:manage'))
const ops = useFileOps('local', () => load())
const opener = useFileOpener('local', (id, p) => desktop.open(id, p))

interface Icon {
  key: string
  label: string
  icon: string
  entry?: FileEntry
  open: () => void
}

const systemIcons = computed<Icon[]>(() => {
  const list: Icon[] = []
  if (canFiles.value) {
    list.push({ key: 'sys:thispc', label: t('explorer.thisPC'), icon: 'thispc', open: () => desktop.open('explorer', { path: 'computer:' }) })
    list.push({ key: 'sys:trash', label: t('explorer.trash'), icon: 'trash', open: () => desktop.open('explorer', { path: 'trash:' }) })
  }
  for (const id of ['terminal', 'monitor']) {
    const a = appMap[id]
    if (a && session.has(a.permission)) list.push({ key: `app:${id}`, label: t(a.title), icon: a.icon, open: () => desktop.open(id) })
  }
  return list
})

const icons = computed<Icon[]>(() => {
  const all: Icon[] = [
    ...systemIcons.value,
    ...files.value
      .filter((f) => prefs.p.explorer.showHidden || !f.name.startsWith('.'))
      .map((f) => ({ key: `file:${f.name}`, label: f.name, icon: fileKind(f.name, f.isDir), entry: f, open: () => opener.open(f) })),
  ]
  const order = desktop.icons
  return all.sort((a, b) => {
    const ia = order.indexOf(a.key)
    const ib = order.indexOf(b.key)
    return (ia < 0 ? 1e6 : ia) - (ib < 0 ? 1e6 : ib)
  })
})

async function load() {
  if (!canFiles.value) return
  try {
    files.value = (await filesApi.list('local', dir.value)).entries
  } catch {
    // 桌面目录不存在时自动创建
    if (canManage.value) {
      try {
        await filesApi.mkdir('local', dir.value)
        files.value = []
      } catch {
        files.value = []
      }
    }
  }
}

let unsubTasks: (() => void) | null = null
let poll: number | undefined
onMounted(() => {
  load()
  // 任务完成（复制、解压等）后刷新；另外每 10 秒刷新一次，反映终端中的修改
  unsubTasks = realtime.subscribe('tasks', () => load())
  poll = window.setInterval(() => {
    if (!document.hidden) load()
  }, 10000)
  window.addEventListener('keydown', onKey)
})
onUnmounted(() => {
  unsubTasks?.()
  clearInterval(poll)
  window.removeEventListener('keydown', onKey)
})
watch(dir, load)

const selectedEntries = computed(() => icons.value.filter((i) => selected.value.has(i.key) && i.entry).map((i) => i.entry!))

function click(e: MouseEvent, ic: Icon) {
  if (e.ctrlKey || e.metaKey) {
    const s = new Set(selected.value)
    if (s.has(ic.key)) s.delete(ic.key)
    else s.add(ic.key)
    selected.value = s
  } else selected.value = new Set([ic.key])
}

// 只在没有活动窗口（焦点在桌面）时响应
function onKey(e: KeyboardEvent) {
  if (desktop.active || (e.target as HTMLElement)?.closest?.('input,textarea,.cm-editor,.xterm')) return
  const entries = selectedEntries.value
  if (e.key === 'Delete' && entries.length && canManage.value) ops.remove(entries, e.shiftKey)
  else if (e.key === 'F2' && entries.length === 1 && canManage.value) ops.rename(entries[0])
  else if (e.key === 'Enter') icons.value.filter((i) => selected.value.has(i.key)).forEach((i) => i.open())
  else if ((e.ctrlKey || e.metaKey) && e.key === 'c') ops.copy(entries)
  else if ((e.ctrlKey || e.metaKey) && e.key === 'x' && canManage.value) ops.cut(entries)
  else if ((e.ctrlKey || e.metaKey) && e.key === 'v' && canManage.value) ops.paste(dir.value)
  else if ((e.ctrlKey || e.metaKey) && e.key === 'a') {
    selected.value = new Set(icons.value.map((i) => i.key))
    e.preventDefault()
  } else return
  e.preventDefault()
}

function showMenu(e: MouseEvent, options: DropdownOption[], onSelect: (k: string) => void) {
  e.preventDefault()
  menu.value = null
  requestAnimationFrame(() => (menu.value = { x: e.clientX, y: e.clientY, options, onSelect }))
}

function iconMenu(e: MouseEvent, ic: Icon) {
  if (!selected.value.has(ic.key)) selected.value = new Set([ic.key])
  e.stopPropagation()
  if (!ic.entry) {
    showMenu(e, [{ key: 'open', label: t('explorer.open') }], () => ic.open())
    return
  }
  const entries = selectedEntries.value
  const m = !canManage.value
  const opts: DropdownOption[] = [
    { key: 'open', label: t('explorer.open') },
    ...(ic.entry.isDir ? [{ key: 'terminal', label: t('explorer.openTerminalHere'), disabled: !session.has('terminal:use') }] : []),
    { type: 'divider', key: 'd1' },
    { key: 'cut', label: t('explorer.cutAction'), disabled: m },
    { key: 'copy', label: t('explorer.copyAction') },
    { key: 'download', label: t('explorer.download') },
    { type: 'divider', key: 'd2' },
    { key: 'compress', label: t('explorer.compress'), disabled: m },
    { key: 'rename', label: t('explorer.rename'), disabled: m || entries.length !== 1 },
    { key: 'delete', label: t('explorer.delete'), disabled: m },
    { type: 'divider', key: 'd3' },
    { key: 'props', label: t('explorer.properties') },
  ]
  showMenu(e, opts, (k) => {
    if (k === 'open') icons.value.filter((i) => selected.value.has(i.key)).forEach((i) => i.open())
    else if (k === 'terminal') desktop.open('terminal', { dir: ic.entry!.path })
    else if (k === 'cut') ops.cut(entries)
    else if (k === 'copy') ops.copy(entries)
    else if (k === 'download') ops.download(entries)
    else if (k === 'compress') ops.compress(dir.value, entries)
    else if (k === 'rename') ops.rename(entries[0])
    else if (k === 'delete') ops.remove(entries)
    else if (k === 'props') ops.properties(entries)
  })
}

function bgMenu(e: MouseEvent) {
  selected.value = new Set()
  const m = !canManage.value || !canFiles.value
  showMenu(
    e,
    [
      { key: 'refresh', label: t('explorer.refresh') },
      { key: 'paste', label: t('explorer.paste'), disabled: m || !clip.mode },
      { type: 'divider', key: 'd1' },
      { key: 'newFolder', label: t('explorer.newFolder'), disabled: m },
      { key: 'newFile', label: t('explorer.newTextFile'), disabled: m },
      { type: 'divider', key: 'd2' },
      { key: 'openDir', label: t('desktop.openDesktopFolder'), disabled: !canFiles.value },
      { key: 'terminal', label: t('explorer.openTerminalHere'), disabled: !session.has('terminal:use') },
      { type: 'divider', key: 'd3' },
      { key: 'hidden', label: prefs.p.explorer.showHidden ? t('explorer.hideHidden') : t('explorer.showHidden') },
      { key: 'personalize', label: t('desktop.personalize') },
    ],
    (k) => {
      if (k === 'refresh') load()
      else if (k === 'paste') ops.paste(dir.value)
      else if (k === 'newFolder') ops.newFolder(dir.value)
      else if (k === 'newFile') ops.newFile(dir.value)
      else if (k === 'openDir') desktop.open('explorer', { path: dir.value })
      else if (k === 'terminal') desktop.open('terminal', { dir: dir.value })
      else if (k === 'hidden') prefs.p.explorer.showHidden = !prefs.p.explorer.showHidden
      else if (k === 'personalize') desktop.open('settings', { section: 'personalize' })
    },
  )
}

/* ---------- 框选 ---------- */
function onBgDown(e: PointerEvent) {
  if (e.button !== 0 || e.target !== root.value) return
  selected.value = new Set()
  const r = root.value!.getBoundingClientRect()
  marquee.value = { x0: e.clientX - r.left, y0: e.clientY - r.top, x1: e.clientX - r.left, y1: e.clientY - r.top }
  root.value!.setPointerCapture(e.pointerId)
}
function onBgMove(e: PointerEvent) {
  if (!marquee.value) return
  const r = root.value!.getBoundingClientRect()
  marquee.value.x1 = e.clientX - r.left
  marquee.value.y1 = e.clientY - r.top
  const box = marqueeBox.value!
  const s = new Set<string>()
  root.value!.querySelectorAll<HTMLElement>('.icon').forEach((el) => {
    const ir = el.getBoundingClientRect()
    const x = ir.left - r.left
    const y = ir.top - r.top
    if (x < box.left + box.width && x + ir.width > box.left && y < box.top + box.height && y + ir.height > box.top) s.add(el.dataset.key!)
  })
  selected.value = s
}
function onBgUp() {
  marquee.value = null
}
const marqueeBox = computed(() => {
  const m = marquee.value
  if (!m) return null
  return { left: Math.min(m.x0, m.x1), top: Math.min(m.y0, m.y1), width: Math.abs(m.x1 - m.x0), height: Math.abs(m.y1 - m.y0) }
})

/* ---------- 拖放：排序、移入文件夹 / 回收站、从电脑拖入上传 ---------- */
const DND = 'application/x-sshdesktop-desktop'

function onDragStart(e: DragEvent, ic: Icon) {
  if (!selected.value.has(ic.key)) selected.value = new Set([ic.key])
  const keys = [...selected.value]
  e.dataTransfer!.setData(DND, JSON.stringify(keys))
  const paths = selectedEntries.value.map((x) => x.path)
  if (paths.length) e.dataTransfer!.setData('application/x-sshdesktop-files', JSON.stringify({ node: 'local', paths }))
  // Chrome 支持把单个文件拖到电脑上直接下载
  if (ic.entry && !ic.entry.isDir && keys.length === 1) {
    e.dataTransfer!.setData('DownloadURL', `application/octet-stream:${ic.entry.name}:${location.origin}${filesApi.downloadURL('local', [ic.entry.path])}`)
  }
  e.dataTransfer!.effectAllowed = 'copyMove'
}

function onDragOver(e: DragEvent, ic?: Icon) {
  e.preventDefault()
  dropHover.value = ic && (ic.entry?.isDir || ic.key === 'sys:trash') ? ic.key : null
}

async function onDrop(e: DragEvent, target?: Icon) {
  e.preventDefault()
  dropHover.value = null
  const dt = e.dataTransfer!
  const internal = dt.getData(DND)
  const filesData = dt.getData('application/x-sshdesktop-files')
  if (!internal && !filesData) {
    if (!canManage.value) return
    const dest = target?.entry?.isDir ? target.entry.path : dir.value
    const items = await collectDropped(dt)
    ops.upload(items, dest)
    return
  }
  const keys: string[] = internal ? JSON.parse(internal) : []
  const moving = filesData ? (JSON.parse(filesData) as { node: string; paths: string[] }) : null
  if (target?.key === 'sys:trash' && moving) {
    ops.remove(moving.paths.map((p) => ({ path: p }) as FileEntry))
    return
  }
  if (target?.entry?.isDir && moving && !moving.paths.includes(target.entry.path)) {
    ops.transfer(moving.paths, target.entry.path, e.ctrlKey ? 'copy' : 'move')
    return
  }
  if (moving && !internal) {
    // 从资源管理器拖到桌面：移动（按住 Ctrl 复制）
    ops.transfer(moving.paths, dir.value, e.ctrlKey ? 'copy' : 'move')
    return
  }
  // 桌面内排序：把拖动的图标放到目标图标之前
  const order = icons.value.map((i) => i.key).filter((k) => !keys.includes(k))
  const idx = target ? order.indexOf(target.key) : order.length
  order.splice(idx < 0 ? order.length : idx, 0, ...keys)
  desktop.icons = order
}

defineExpose({ reload: load })
</script>

<template>
  <div
    ref="root"
    class="desk"
    data-testid="desktop"
    @contextmenu="bgMenu"
    @pointerdown="onBgDown"
    @pointermove="onBgMove"
    @pointerup="onBgUp"
    @dragover="onDragOver($event)"
    @drop="onDrop($event)"
  >
    <div
      v-for="ic in icons"
      :key="ic.key"
      class="icon"
      :class="{ sel: selected.has(ic.key), cut: clip.mode === 'move' && ic.entry && clip.paths.includes(ic.entry.path), drop: dropHover === ic.key }"
      :data-key="ic.key"
      :data-testid="'desk-icon-' + ic.label"
      draggable="true"
      :title="ic.entry ? ic.entry.path : ic.label"
      @click.stop="click($event, ic)"
      @dblclick.stop="ic.open()"
      @contextmenu="iconMenu($event, ic)"
      @dragstart="onDragStart($event, ic)"
      @dragover.stop="onDragOver($event, ic)"
      @drop.stop="onDrop($event, ic)"
      @pointerdown.stop
    >
      <SdIcon :name="ic.key === 'sys:trash' ? 'trash' : ic.icon" :size="44" />
      <span class="label">{{ ic.label }}</span>
      <span v-if="ic.entry?.type === 'link'" class="link">↗</span>
    </div>
    <div v-if="marqueeBox" class="marquee" :style="{ left: marqueeBox.left + 'px', top: marqueeBox.top + 'px', width: marqueeBox.width + 'px', height: marqueeBox.height + 'px' }" />
    <NDropdown
      v-if="menu"
      :show="true"
      trigger="manual"
      placement="bottom-start"
      :x="menu.x"
      :y="menu.y"
      :options="menu.options"
      @clickoutside="menu = null"
      @select="(k: string) => { const m = menu!; menu = null; m.onSelect(k) }"
    />
  </div>
</template>

<style scoped>
.desk {
  position: absolute;
  inset: 0 0 48px 0;
  padding: 6px;
  display: grid;
  grid-auto-flow: column;
  grid-template-rows: repeat(auto-fill, 100px);
  grid-auto-columns: 84px;
  gap: 2px;
  align-content: start;
  user-select: none;
}
.icon {
  position: relative;
  width: 82px;
  height: 98px;
  padding: 6px 2px 2px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  border-radius: 4px;
  border: 1px solid transparent;
  color: #fff;
}
.icon:hover {
  background: rgba(255, 255, 255, 0.12);
}
.icon.sel {
  background: rgba(255, 255, 255, 0.22);
  border-color: rgba(255, 255, 255, 0.35);
}
.icon.drop {
  background: rgba(0, 120, 212, 0.35);
}
.icon.cut {
  opacity: 0.5;
}
.label {
  font-size: 12px;
  line-height: 1.25;
  text-align: center;
  text-shadow: 0 0 3px rgba(0, 0, 0, 0.9), 0 1px 2px rgba(0, 0, 0, 0.8);
  max-width: 100%;
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  word-break: break-all;
}
.icon.sel .label {
  -webkit-line-clamp: 5;
}
.link {
  position: absolute;
  left: 20px;
  top: 34px;
  font-size: 11px;
  background: #fff;
  color: #0067c0;
  border-radius: 2px;
  padding: 0 2px;
}
.marquee {
  position: absolute;
  border: 1px solid rgba(0, 120, 212, 0.9);
  background: rgba(0, 120, 212, 0.25);
  pointer-events: none;
}
</style>
