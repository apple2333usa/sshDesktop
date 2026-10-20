<script setup lang="ts">
// 文件资源管理器：导航窗格 + 地址栏 + 工具栏 + 文件列表，风格参考 Windows 11。
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDropdown, NInput, NTooltip, useMessage, type DropdownOption } from 'naive-ui'
import { filesApi } from '@/api/files'
import type { FileEntry } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { usePrefs } from '@/stores/prefs'
import { useSession } from '@/stores/session'
import { useNotify } from '@/stores/notify'
import { useFileClipboard } from '@/stores/clipboard'
import { errorText, formatBytes } from '@/i18n'
import { fileKind } from '@/components/icons'
import SdIcon from '@/components/SdIcon.vue'
import FileList, { type SortKey } from './FileList.vue'
import ComputerView from './ComputerView.vue'
import TrashView from './TrashView.vue'
import { collectDropped, join, parentOf, useFileOps } from './fileops'
import { useFileOpener } from './opener'

const COMPUTER = 'computer:'
const TRASH = 'trash:'
const FILES_DND = 'application/x-sshdesktop-files'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const prefs = usePrefs()
const session = useSession()
const notify = useNotify()
const clip = useFileClipboard()

const node = (host.props.node as string) || 'local'
const path = ref<string>((host.props.path as string) || '/root')
const back = ref<string[]>([])
const forward = ref<string[]>([])
const entries = ref<FileEntry[]>([])
const loading = ref(false)
const error = ref('')
const selected = ref(new Set<string>())
const sort = ref<{ key: SortKey; desc: boolean }>({ key: 'name', desc: false })
const filter = ref('')
const searchResults = ref<FileEntry[] | null>(null)
const searching = ref(false)
let searchCtl: AbortController | null = null
const editingAddress = ref(false)
const addressText = ref('')
const dropTarget = ref<string | null>(null)
const root = ref<HTMLElement | null>(null)
const trashView = ref<InstanceType<typeof TrashView> | null>(null)

const canManage = computed(() => session.has('files:manage'))
const special = computed(() => path.value === COMPUTER || path.value === TRASH)
const view = computed({
  get: () => prefs.p.explorer.view,
  set: (v) => (prefs.p.explorer.view = v),
})

/* ---------- 加载与导航 ---------- */
let loadSeq = 0
async function load() {
  if (special.value) {
    entries.value = []
    if (path.value === TRASH) trashView.value?.reload()
    return
  }
  const seq = ++loadSeq
  loading.value = true
  try {
    const r = await filesApi.list(node, path.value)
    if (seq !== loadSeq) return
    entries.value = r.entries
    error.value = ''
    const keep = new Set([...selected.value].filter((p) => r.entries.some((e) => e.path === p)))
    selected.value = keep
  } catch (e) {
    if (seq !== loadSeq) return
    entries.value = []
    error.value = errorText(e)
  } finally {
    if (seq === loadSeq) loading.value = false
  }
}

function navigate(p: string, record = true) {
  if (!p) return
  if (!special.value || p !== path.value) {
    if (record && p !== path.value) {
      back.value.push(path.value)
      if (back.value.length > 50) back.value.shift()
      forward.value = []
    }
  }
  path.value = p
  filter.value = ''
  searchResults.value = null
  searchCtl?.abort()
  selected.value = new Set()
  editingAddress.value = false
}

function goBack() {
  const p = back.value.pop()
  if (p === undefined) return
  forward.value.push(path.value)
  navigate(p, false)
}
function goForward() {
  const p = forward.value.pop()
  if (p === undefined) return
  back.value.push(path.value)
  navigate(p, false)
}
function goUp() {
  if (special.value) return
  if (path.value === '/') navigate(COMPUTER)
  else navigate(parentOf(path.value))
}

watch(
  path,
  (p) => {
    host.setState({ path: p, node })
    host.setTitle(p === COMPUTER ? t('explorer.thisPC') : p === TRASH ? t('explorer.trash') : p === '/' ? '/' : p.slice(p.lastIndexOf('/') + 1))
    load()
  },
  { immediate: true },
)

// 后台任务（粘贴、压缩、解压等）完成后刷新当前目录
watch(
  () => notify.list.filter((n) => n.done).length,
  () => load(),
)

/* ---------- 地址栏 ---------- */
const crumbs = computed(() => {
  if (path.value === COMPUTER) return [{ label: t('explorer.thisPC'), path: COMPUTER }]
  if (path.value === TRASH) return [{ label: t('explorer.trash'), path: TRASH }]
  const out = [{ label: t('explorer.thisPC'), path: COMPUTER }, { label: '/', path: '/' }]
  let cur = ''
  for (const part of path.value.split('/').filter(Boolean)) {
    cur += '/' + part
    out.push({ label: part, path: cur })
  }
  return out
})

const addressInput = ref<InstanceType<typeof NInput> | null>(null)
function editAddress() {
  addressText.value = special.value ? '' : path.value
  editingAddress.value = true
  nextTick(() => addressInput.value?.select())
}
function commitAddress() {
  let p = addressText.value.trim()
  editingAddress.value = false
  if (!p) return
  if (p === '~') p = '/root'
  if (!p.startsWith('/')) p = join(special.value ? '/' : path.value, p)
  p = p.replace(/\/+$/, '') || '/'
  navigate(p)
}

/* ---------- 搜索 ---------- */
async function deepSearch() {
  const q = filter.value.trim()
  if (!q || special.value) return
  searchCtl?.abort()
  searchCtl = new AbortController()
  searching.value = true
  try {
    const r = await filesApi.search(node, path.value, q, searchCtl.signal)
    searchResults.value = r.entries
    if (r.partial) message.info(t('explorer.searchPartial'))
  } catch (e) {
    if ((e as Error).name !== 'AbortError') message.error(errorText(e))
  } finally {
    searching.value = false
  }
}
watch(filter, (v) => {
  if (!v) searchResults.value = null
})

/* ---------- 排序与过滤 ---------- */
const shown = computed(() => {
  let list = searchResults.value ?? entries.value
  if (!prefs.p.explorer.showHidden && !searchResults.value) list = list.filter((e) => !e.name.startsWith('.'))
  const q = filter.value.trim().toLowerCase()
  if (q && !searchResults.value) list = list.filter((e) => e.name.toLowerCase().includes(q))
  const { key, desc } = sort.value
  const dir = desc ? -1 : 1
  const coll = new Intl.Collator('zh-CN', { numeric: true, sensitivity: 'base' })
  return [...list].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
    let r = 0
    if (key === 'mtime') r = a.mtime - b.mtime
    else if (key === 'size') r = a.size - b.size
    else if (key === 'type') r = coll.compare(fileKind(a.name, a.isDir), fileKind(b.name, b.isDir))
    return (r || coll.compare(a.name, b.name)) * dir
  })
})

function setSort(k: SortKey) {
  sort.value = sort.value.key === k ? { key: k, desc: !sort.value.desc } : { key: k, desc: false }
}

const selectedEntries = computed(() => shown.value.filter((e) => selected.value.has(e.path)))
const single = computed(() => (selectedEntries.value.length === 1 ? selectedEntries.value[0] : null))

/* ---------- 文件操作 ---------- */
const ops = useFileOps(node, load)
const opener = useFileOpener(node, host.openApp)

function open(e: FileEntry) {
  if (e.isDir || (e.type === 'link' && !e.broken && e.isDir)) navigate(e.path)
  else opener.open(e)
}

async function newFolder() {
  const name = await ops.newFolder(path.value)
  if (name) selected.value = new Set([join(path.value, name)])
}
async function newFile() {
  const name = await ops.newFile(path.value)
  if (name) selected.value = new Set([join(path.value, name)])
}

const fileInput = ref<HTMLInputElement | null>(null)
const folderInput = ref<HTMLInputElement | null>(null)
function onPick(ev: Event) {
  const input = ev.target as HTMLInputElement
  const files = Array.from(input.files ?? [])
  const items = files.map((f) => ({ file: f, rel: (f as File & { webkitRelativePath?: string }).webkitRelativePath || f.name }))
  input.value = ''
  ops.upload(items, path.value)
}

function copyPath() {
  const text = selectedEntries.value.length ? selectedEntries.value.map((e) => e.path).join('\n') : path.value
  navigator.clipboard?.writeText(text).then(
    () => message.success(t('explorer.pathCopied')),
    () => message.error(t('explorer.clipboardDenied')),
  )
}

function toggleHidden() {
  prefs.p.explorer.showHidden = !prefs.p.explorer.showHidden
}

/* ---------- 菜单 ---------- */
const menu = ref<{ x: number; y: number; options: DropdownOption[] } | null>(null)

function itemMenuOptions(): DropdownOption[] {
  const sel = selectedEntries.value
  const one = single.value
  const m = canManage.value
  const opts: DropdownOption[] = []
  if (one) {
    opts.push({ key: 'open', label: t('explorer.open') })
    if (!one.isDir) {
      opts.push({
        key: 'openWith',
        label: t('explorer.openWith'),
        children: [
          { key: 'openEditor', label: t('apps.editor') },
          { key: 'openViewer', label: t('apps.viewer') },
        ],
      })
      if (fileKind(one.name, false) === 'archive') opts.push({ key: 'extract', label: t('explorer.extract'), disabled: !m })
    } else {
      opts.push({ key: 'openNew', label: t('explorer.openNewWindow') })
      opts.push({ key: 'terminalHere', label: t('explorer.openTerminalHere'), disabled: !session.has('terminal:use') })
    }
    opts.push({ type: 'divider', key: 'd0' })
  }
  opts.push(
    { key: 'cut', label: t('explorer.cutAction'), disabled: !m },
    { key: 'copy', label: t('explorer.copyAction') },
  )
  if (one?.isDir) opts.push({ key: 'pasteInto', label: t('explorer.pasteInto'), disabled: !m || !clip.mode })
  opts.push(
    { key: 'copyPath', label: t('explorer.copyPath') },
    { type: 'divider', key: 'd1' },
    { key: 'download', label: t('explorer.download') },
    { key: 'compress', label: t('explorer.compress'), disabled: !m },
    { type: 'divider', key: 'd2' },
    { key: 'rename', label: t('explorer.rename'), disabled: !m || !one },
    { key: 'delete', label: t('explorer.delete'), disabled: !m },
    { key: 'deletePermanent', label: t('explorer.deletePermanently'), disabled: !m },
    { type: 'divider', key: 'd3' },
    { key: 'properties', label: t('explorer.properties'), disabled: !sel.length },
  )
  return opts
}

function bgMenuOptions(): DropdownOption[] {
  const m = canManage.value
  return [
    {
      key: 'view',
      label: t('explorer.view'),
      children: [
        { key: 'view:icons', label: t('explorer.viewIcons') },
        { key: 'view:list', label: t('explorer.viewList') },
        { key: 'view:details', label: t('explorer.viewDetails') },
      ],
    },
    {
      key: 'sort',
      label: t('explorer.sortBy'),
      children: (['name', 'mtime', 'type', 'size'] as SortKey[]).map((k) => ({ key: 'sort:' + k, label: sortLabel(k) })),
    },
    { key: 'refresh', label: t('explorer.refresh') },
    { type: 'divider', key: 'd0' },
    { key: 'paste', label: t('explorer.paste'), disabled: !m || !clip.mode },
    {
      key: 'new',
      label: t('explorer.new'),
      disabled: !m,
      children: [
        { key: 'newFolder', label: t('explorer.newFolder') },
        { key: 'newFile', label: t('explorer.newFile') },
      ],
    },
    { key: 'upload', label: t('explorer.uploadFiles'), disabled: !m },
    { key: 'uploadFolder', label: t('explorer.uploadFolder'), disabled: !m },
    { type: 'divider', key: 'd1' },
    { key: 'terminalHere', label: t('explorer.openTerminalHere'), disabled: !session.has('terminal:use') },
    { key: 'copyPath', label: t('explorer.copyPath') },
    { key: 'toggleHidden', label: prefs.p.explorer.showHidden ? t('explorer.hideHidden') : t('explorer.showHidden') },
    { key: 'properties', label: t('explorer.properties') },
  ]
}

function sortLabel(k: SortKey) {
  const names: Record<SortKey, string> = {
    name: t('explorer.name'),
    mtime: t('explorer.modified'),
    type: t('explorer.type'),
    size: t('explorer.size'),
  }
  return names[k] + (sort.value.key === k ? (sort.value.desc ? ' ↓' : ' ↑') : '')
}

function onMenu(ev: MouseEvent, e: FileEntry | null) {
  if (special.value) return
  menu.value = { x: ev.clientX, y: ev.clientY, options: e ? itemMenuOptions() : bgMenuOptions() }
}

function act(key: string) {
  menu.value = null
  const sel = selectedEntries.value
  const one = single.value
  if (key.startsWith('view:')) view.value = key.slice(5) as typeof view.value
  else if (key.startsWith('sort:')) setSort(key.slice(5) as SortKey)
  else
    switch (key) {
      case 'open':
        if (one) open(one)
        break
      case 'openEditor':
      case 'openViewer':
        if (one) opener.open(one, key === 'openEditor' ? 'editor' : 'viewer')
        break
      case 'openNew':
        if (one) host.openApp('explorer', { path: one.path, node })
        break
      case 'extract':
        if (one) opener.extract(one).then(load)
        break
      case 'terminalHere':
        host.openApp('terminal', { cwd: one?.isDir ? one.path : special.value ? '/root' : path.value })
        break
      case 'cut':
        ops.cut(sel)
        break
      case 'copy':
        ops.copy(sel)
        break
      case 'paste':
        ops.paste(path.value)
        break
      case 'pasteInto':
        if (one) ops.paste(one.path)
        break
      case 'copyPath':
        copyPath()
        break
      case 'download':
        ops.download(sel)
        break
      case 'compress':
        ops.compress(path.value, sel)
        break
      case 'rename':
        if (one) ops.rename(one)
        break
      case 'delete':
        ops.remove(sel)
        break
      case 'deletePermanent':
        ops.remove(sel, true)
        break
      case 'properties':
        if (sel.length) ops.properties(sel)
        else filesApi.stat(node, path.value).then((e) => ops.properties([e]), (e) => message.error(errorText(e)))
        break
      case 'refresh':
        load()
        break
      case 'newFolder':
        newFolder()
        break
      case 'newFile':
        newFile()
        break
      case 'upload':
        fileInput.value?.click()
        break
      case 'uploadFolder':
        folderInput.value?.click()
        break
      case 'toggleHidden':
        toggleHidden()
        break
    }
}

const newOptions = computed<DropdownOption[]>(() => [
  { key: 'newFolder', label: t('explorer.newFolder') },
  { key: 'newFile', label: t('explorer.newFile') },
])
const uploadOptions = computed<DropdownOption[]>(() => [
  { key: 'upload', label: t('explorer.uploadFiles') },
  { key: 'uploadFolder', label: t('explorer.uploadFolder') },
])
const sortOptions = computed<DropdownOption[]>(() =>
  (['name', 'mtime', 'type', 'size'] as SortKey[]).map((k) => ({ key: 'sort:' + k, label: sortLabel(k) })),
)
const viewOptions = computed<DropdownOption[]>(() => [
  { key: 'view:icons', label: t('explorer.viewIcons') },
  { key: 'view:list', label: t('explorer.viewList') },
  { key: 'view:details', label: t('explorer.viewDetails') },
  { type: 'divider', key: 'd' },
  { key: 'toggleHidden', label: prefs.p.explorer.showHidden ? t('explorer.hideHidden') : t('explorer.showHidden') },
])
const moreOptions = computed<DropdownOption[]>(() => {
  const m = canManage.value
  const sel = selectedEntries.value
  const one = single.value
  return [
    { key: 'compress', label: t('explorer.compress'), disabled: !m || !sel.length },
    { key: 'extract', label: t('explorer.extract'), disabled: !m || !one || fileKind(one.name, one.isDir) !== 'archive' },
    { key: 'deletePermanent', label: t('explorer.deletePermanently'), disabled: !m || !sel.length },
    { type: 'divider', key: 'd' },
    { key: 'terminalHere', label: t('explorer.openTerminalHere'), disabled: !session.has('terminal:use') },
    { key: 'copyPath', label: t('explorer.copyPath') },
    { key: 'properties', label: t('explorer.properties') },
  ]
})

/* ---------- 拖放 ---------- */
function onDragStart(ev: DragEvent, e: FileEntry) {
  if (!selected.value.has(e.path)) selected.value = new Set([e.path])
  const paths = selectedEntries.value.map((x) => x.path)
  const dt = ev.dataTransfer!
  dt.setData(FILES_DND, JSON.stringify({ node, paths }))
  dt.setData('text/plain', paths.join('\n'))
  if (!e.isDir && paths.length === 1) {
    dt.setData('DownloadURL', `application/octet-stream:${e.name}:${location.origin}${filesApi.downloadURL(node, [e.path])}`)
  }
  dt.effectAllowed = 'copyMove'
}

function onDragOver(ev: DragEvent, e: FileEntry | null) {
  if (special.value && path.value !== TRASH) return
  const target = e?.isDir ? e.path : null
  dropTarget.value = target && !selected.value.has(target) ? target : null
  if (ev.dataTransfer) ev.dataTransfer.dropEffect = ev.ctrlKey ? 'copy' : 'move'
}

async function dropTo(ev: DragEvent, dest: string | 'trash') {
  dropTarget.value = null
  const dt = ev.dataTransfer
  if (!dt || !canManage.value) return
  const raw = dt.getData(FILES_DND)
  if (raw) {
    const data = JSON.parse(raw) as { node: string; paths: string[] }
    if (dest === 'trash') {
      ops.remove(data.paths.map((p) => ({ path: p }) as FileEntry))
      return
    }
    const sources = data.paths.filter((p) => p !== dest && parentOf(p) !== dest)
    if (!sources.length) return
    ops.transfer(sources, dest, ev.ctrlKey ? 'copy' : 'move')
    return
  }
  if (dest === 'trash') return
  const items = await collectDropped(dt)
  ops.upload(items, dest)
}

function onDrop(ev: DragEvent, e: FileEntry | null) {
  if (special.value) return
  dropTo(ev, e?.isDir ? e.path : path.value)
}

/* ---------- 键盘 ---------- */
function onKey(ev: KeyboardEvent) {
  if (special.value || editingAddress.value) return
  const target = ev.target as HTMLElement
  if (target.closest('input, textarea, [contenteditable]')) return
  const ctrl = ev.ctrlKey || ev.metaKey
  const k = ev.key
  const sel = selectedEntries.value
  let handled = true
  if (ctrl && k.toLowerCase() === 'a') selected.value = new Set(shown.value.map((e) => e.path))
  else if (ctrl && k.toLowerCase() === 'c') ops.copy(sel)
  else if (ctrl && k.toLowerCase() === 'x' && canManage.value) ops.cut(sel)
  else if (ctrl && k.toLowerCase() === 'v' && canManage.value) ops.paste(path.value)
  else if (k === 'Delete' && canManage.value && sel.length) ops.remove(sel, ev.shiftKey)
  else if (k === 'F2' && canManage.value && single.value) ops.rename(single.value)
  else if (k === 'F5') load()
  else if (k === 'Enter' && single.value) open(single.value)
  else if (k === 'Backspace' || (ev.altKey && k === 'ArrowUp')) goUp()
  else if (ev.altKey && k === 'ArrowLeft') goBack()
  else if (ev.altKey && k === 'ArrowRight') goForward()
  else if (k === 'ArrowDown' || k === 'ArrowUp') moveSelection(k === 'ArrowDown' ? 1 : -1, ev.shiftKey)
  else handled = false
  if (handled) {
    ev.preventDefault()
    ev.stopPropagation()
  }
}

function moveSelection(delta: number, extend: boolean) {
  const list = shown.value
  if (!list.length) return
  const idx = list.findIndex((e) => selected.value.has(e.path))
  const last = [...list].reverse().findIndex((e) => selected.value.has(e.path))
  const cur = delta > 0 ? (last < 0 ? -1 : list.length - 1 - last) : idx < 0 ? list.length : idx
  const next = Math.max(0, Math.min(list.length - 1, cur + delta))
  const s = extend ? new Set(selected.value) : new Set<string>()
  s.add(list[next].path)
  selected.value = s
  nextTick(() => root.value?.querySelector(`[data-path="${CSS.escape(list[next].path)}"]`)?.scrollIntoView({ block: 'nearest' }))
}

onMounted(() => root.value?.focus())

/* ---------- 导航窗格 ---------- */
const quick = computed(() => [
  { label: t('explorer.desktop'), path: prefs.p.desktopDir, icon: 'folder' },
  { label: 'root', path: '/root', icon: 'folder' },
  { label: '/', path: '/', icon: 'diskSystem' },
  { label: 'etc', path: '/etc', icon: 'folder' },
  { label: 'log', path: '/var/log', icon: 'folder' },
  { label: 'tmp', path: '/tmp', icon: 'folder' },
])

const status = computed(() => {
  if (path.value === COMPUTER || path.value === TRASH) return ''
  const n = shown.value.length
  const sel = selectedEntries.value
  let s = t('explorer.itemsN', { n })
  if (sel.length) {
    s += '　' + t('explorer.selectedN', { n: sel.length })
    const size = sel.filter((e) => !e.isDir).reduce((a, e) => a + e.size, 0)
    if (size) s += '　' + formatBytes(size)
  }
  return s
})

onUnmounted(() => searchCtl?.abort())
</script>

<template>
  <div ref="root" class="explorer" tabindex="0" data-testid="explorer" @keydown="onKey" @click="menu = null">
    <!-- 地址栏 -->
    <div class="nav">
      <NTooltip :delay="600">
        <template #trigger>
          <NButton quaternary size="small" :disabled="!back.length" @click="goBack">←</NButton>
        </template>
        {{ t('explorer.back') }} (Alt+←)
      </NTooltip>
      <NButton quaternary size="small" :disabled="!forward.length" :title="t('explorer.forward')" @click="goForward">→</NButton>
      <NButton quaternary size="small" :disabled="special" :title="t('explorer.up')" data-testid="explorer-up" @click="goUp">↑</NButton>
      <NButton quaternary size="small" :title="t('explorer.refresh') + ' (F5)'" @click="load">⟳</NButton>
      <div class="address" @click.self="editAddress">
        <NInput
          v-if="editingAddress"
          ref="addressInput"
          v-model:value="addressText"
          size="small"
          data-testid="explorer-address"
          @keydown.enter.prevent="commitAddress"
          @keydown.esc.prevent="editingAddress = false"
          @blur="editingAddress = false"
        />
        <template v-else>
          <span
            v-for="(c, i) in crumbs"
            :key="c.path"
            class="crumb"
            @click.stop="navigate(c.path)"
            @dragover.prevent="dropTarget = c.path"
            @drop.prevent.stop="!c.path.endsWith(':') && dropTo($event, c.path)"
          >
            <span>{{ c.label }}</span><span v-if="i < crumbs.length - 1" class="sep">›</span>
          </span>
          <span class="address-fill" data-testid="explorer-address-edit" @click.stop="editAddress" />
        </template>
      </div>
      <NInput
        v-model:value="filter"
        size="small"
        class="search"
        clearable
        :disabled="special"
        :loading="searching"
        :placeholder="t('explorer.searchPlaceholder')"
        data-testid="explorer-search"
        @keydown.enter.prevent="deepSearch"
      />
    </div>

    <!-- 工具栏 -->
    <div class="tools">
      <NDropdown trigger="click" :options="newOptions" :disabled="!canManage || special" @select="act">
        <NButton size="small" :disabled="!canManage || special" data-testid="explorer-new">＋ {{ t('explorer.new') }} ▾</NButton>
      </NDropdown>
      <span class="divider" />
      <NButton quaternary size="small" :disabled="!canManage || !selectedEntries.length" :title="t('explorer.cutAction') + ' (Ctrl+X)'" @click="act('cut')">✂</NButton>
      <NButton quaternary size="small" :disabled="!selectedEntries.length" :title="t('explorer.copyAction') + ' (Ctrl+C)'" @click="act('copy')">⧉</NButton>
      <NButton quaternary size="small" :disabled="!canManage || !clip.mode || special" :title="t('explorer.paste') + ' (Ctrl+V)'" data-testid="explorer-paste" @click="act('paste')">📋</NButton>
      <NButton quaternary size="small" :disabled="!canManage || !single" :title="t('explorer.rename') + ' (F2)'" @click="act('rename')">✎</NButton>
      <NButton quaternary size="small" :disabled="!canManage || !selectedEntries.length" :title="t('explorer.delete') + ' (Delete)'" data-testid="explorer-delete" @click="act('delete')">🗑</NButton>
      <span class="divider" />
      <NDropdown trigger="click" :options="sortOptions" @select="act">
        <NButton quaternary size="small" :disabled="special">⇅ {{ t('explorer.sort') }} ▾</NButton>
      </NDropdown>
      <NDropdown trigger="click" :options="viewOptions" @select="act">
        <NButton quaternary size="small" :disabled="special">☷ {{ t('explorer.view') }} ▾</NButton>
      </NDropdown>
      <span class="divider" />
      <NDropdown trigger="click" :options="uploadOptions" :disabled="!canManage || special" @select="act">
        <NButton quaternary size="small" :disabled="!canManage || special" data-testid="explorer-upload">⤒ {{ t('explorer.upload') }} ▾</NButton>
      </NDropdown>
      <NButton quaternary size="small" :disabled="!selectedEntries.length" @click="act('download')">⤓ {{ t('explorer.download') }}</NButton>
      <NDropdown trigger="click" :options="moreOptions" :disabled="special" @select="act">
        <NButton quaternary size="small" :disabled="special">⋯</NButton>
      </NDropdown>
      <input ref="fileInput" type="file" multiple hidden data-testid="explorer-file-input" @change="onPick" />
      <input ref="folderInput" type="file" webkitdirectory hidden @change="onPick" />
    </div>

    <div class="body">
      <!-- 导航窗格 -->
      <div class="pane sd-scroll">
        <div class="pane-title">{{ t('explorer.quickAccess') }}</div>
        <div
          v-for="q in quick"
          :key="q.path"
          class="pane-item"
          :class="{ active: path === q.path, drop: dropTarget === 'q:' + q.path }"
          @click="navigate(q.path)"
          @dragover.prevent="dropTarget = 'q:' + q.path"
          @dragleave="dropTarget = null"
          @drop.prevent="dropTo($event, q.path)"
        >
          <SdIcon :name="q.icon" :size="18" /><span class="sd-ellipsis">{{ q.label }}</span>
        </div>
        <div class="pane-sep" />
        <div class="pane-item" :class="{ active: path === COMPUTER }" data-testid="nav-computer" @click="navigate(COMPUTER)">
          <SdIcon name="thispc" :size="18" /><span>{{ t('explorer.thisPC') }}</span>
        </div>
        <div class="pane-item disabled" :title="t('explorer.networkSoon')">
          <SdIcon name="network" :size="18" /><span>{{ t('explorer.network') }}</span>
        </div>
        <div
          class="pane-item"
          :class="{ active: path === TRASH, drop: dropTarget === 'trash' }"
          data-testid="nav-trash"
          @click="navigate(TRASH)"
          @dragover.prevent="dropTarget = 'trash'"
          @dragleave="dropTarget = null"
          @drop.prevent="dropTo($event, 'trash')"
        >
          <SdIcon name="trash" :size="18" /><span>{{ t('explorer.trash') }}</span>
        </div>
      </div>

      <!-- 内容 -->
      <div class="content">
        <ComputerView v-if="path === COMPUTER" :node="node" @open="navigate" />
        <TrashView v-else-if="path === TRASH" ref="trashView" :node="node" />
        <div v-else-if="error" class="error">
          <div>{{ error }}</div>
          <NButton size="small" @click="load">{{ t('explorer.retry') }}</NButton>
        </div>
        <FileList
          v-else
          :entries="shown"
          :selected="selected"
          :view="view"
          :sort="sort"
          :cut-paths="clip.mode === 'move' && clip.node === node ? clip.paths : []"
          :drop-target="dropTarget"
          :show-path="!!searchResults"
          :class="{ loading }"
          @select="(s) => (selected = s)"
          @open="open"
          @menu="onMenu"
          @sort="setSort"
          @dragstart="onDragStart"
          @dragover="onDragOver"
          @drop="onDrop"
        />
      </div>
    </div>

    <div class="statusbar sd-muted">
      <span>{{ status }}</span>
      <span v-if="searchResults" class="sd-muted">{{ t('explorer.searchResults', { q: filter }) }}</span>
      <span v-if="!canManage" class="ro">{{ t('explorer.readOnly') }}</span>
    </div>

    <NDropdown
      v-if="menu"
      trigger="manual"
      placement="bottom-start"
      :show="true"
      :x="menu.x"
      :y="menu.y"
      :options="menu.options"
      @select="act"
      @clickoutside="menu = null"
    />
  </div>
</template>

<style scoped>
.explorer {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--sd-surface);
  outline: none;
}
.nav {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 6px 8px;
}
.address {
  flex: 1;
  min-width: 0;
  height: 28px;
  display: flex;
  align-items: center;
  margin: 0 6px;
  padding: 0 6px;
  border: 1px solid var(--sd-border-strong);
  border-radius: 4px;
  background: var(--sd-surface-2);
  overflow: hidden;
  white-space: nowrap;
  font-size: 13px;
}
.address :deep(.n-input) {
  margin: 0 -6px;
}
.crumb {
  cursor: default;
  display: inline-flex;
  align-items: center;
}
.crumb > span:first-child {
  padding: 2px 4px;
  border-radius: 3px;
}
.crumb > span:first-child:hover {
  background: var(--sd-hover);
}
.sep {
  padding: 0 2px;
  color: var(--sd-text-2);
}
.address-fill {
  flex: 1;
  align-self: stretch;
}
.search {
  width: 220px;
  flex-shrink: 0;
}
.tools {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 4px 8px;
  border-top: 1px solid var(--sd-border);
  border-bottom: 1px solid var(--sd-border);
  overflow-x: auto;
  scrollbar-width: none;
}
.divider {
  width: 1px;
  height: 20px;
  background: var(--sd-border-strong);
  margin: 0 6px;
}
.body {
  flex: 1;
  min-height: 0;
  display: flex;
}
.pane {
  width: 188px;
  flex-shrink: 0;
  padding: 6px 4px;
  border-right: 1px solid var(--sd-border);
  font-size: 13px;
}
.pane-title {
  padding: 4px 10px;
  font-size: 12px;
  color: var(--sd-text-2);
}
.pane-item {
  display: flex;
  align-items: center;
  gap: 8px;
  height: 30px;
  padding: 0 10px;
  border-radius: 4px;
  cursor: default;
}
.pane-item:hover {
  background: var(--sd-hover);
}
.pane-item.active {
  background: var(--sd-select);
}
.pane-item.drop {
  background: var(--sd-accent-soft);
  outline: 1px solid var(--sd-accent);
}
.pane-item.disabled {
  opacity: 0.5;
}
.pane-sep {
  height: 1px;
  background: var(--sd-border);
  margin: 6px 8px;
}
.content {
  flex: 1;
  min-width: 0;
  position: relative;
  overflow: hidden;
}
.content > * {
  height: 100%;
}
.loading {
  opacity: 0.6;
  transition: opacity 0.2s 0.15s;
}
.error {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 12px;
  color: var(--sd-danger);
}
.statusbar {
  height: 24px;
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 0 12px;
  font-size: 12px;
  border-top: 1px solid var(--sd-border);
}
.ro {
  margin-left: auto;
  color: var(--sd-warning);
}
@media (max-width: 640px) {
  .pane {
    display: none;
  }
  .search {
    width: 120px;
  }
}
</style>
