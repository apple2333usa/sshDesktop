<script setup lang="ts">
// 文件列表：大图标、列表、详细信息三种视图；支持多选、框选、拖放。
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import type { FileEntry } from '@/api/types'
import { fileKind } from '@/components/icons'
import SdIcon from '@/components/SdIcon.vue'
import { formatBytes, formatTime } from '@/i18n'

export type SortKey = 'name' | 'mtime' | 'type' | 'size'

const props = defineProps<{
  entries: FileEntry[]
  selected: Set<string>
  view: 'details' | 'list' | 'icons'
  sort: { key: SortKey; desc: boolean }
  cutPaths: string[]
  dropTarget: string | null
  showPath?: boolean
}>()

const emit = defineEmits<{
  select: [Set<string>, string | null]
  open: [FileEntry]
  menu: [MouseEvent, FileEntry | null]
  sort: [SortKey]
  dragstart: [DragEvent, FileEntry]
  dragover: [DragEvent, FileEntry | null]
  drop: [DragEvent, FileEntry | null]
}>()

const { t } = useI18n()
const root = ref<HTMLElement | null>(null)
let anchor: string | null = null

function typeLabel(e: FileEntry) {
  if (e.isDir) return t('explorer.types.dir')
  const kind = fileKind(e.name, false)
  return t('explorer.kinds.' + kind)
}

function click(ev: MouseEvent, e: FileEntry) {
  const s = new Set(props.selected)
  if (ev.shiftKey && anchor) {
    const paths = props.entries.map((x) => x.path)
    const a = paths.indexOf(anchor)
    const b = paths.indexOf(e.path)
    if (a >= 0 && b >= 0) {
      if (!(ev.ctrlKey || ev.metaKey)) s.clear()
      for (let i = Math.min(a, b); i <= Math.max(a, b); i++) s.add(paths[i])
      emit('select', s, e.path)
      return
    }
  }
  if (ev.ctrlKey || ev.metaKey) {
    if (s.has(e.path)) s.delete(e.path)
    else s.add(e.path)
  } else {
    s.clear()
    s.add(e.path)
  }
  anchor = e.path
  emit('select', s, e.path)
}

function onMenu(ev: MouseEvent, e: FileEntry | null) {
  ev.preventDefault()
  ev.stopPropagation()
  if (e && !props.selected.has(e.path)) {
    anchor = e.path
    emit('select', new Set([e.path]), e.path)
  }
  emit('menu', ev, e)
}

/* 框选 */
const box = ref<{ x0: number; y0: number; x1: number; y1: number } | null>(null)
let base = new Set<string>()
function onDown(ev: PointerEvent) {
  if (ev.button !== 0 || (ev.target as HTMLElement).closest('.item, .th')) return
  const r = root.value!.getBoundingClientRect()
  const x = ev.clientX - r.left + root.value!.scrollLeft
  const y = ev.clientY - r.top + root.value!.scrollTop
  box.value = { x0: x, y0: y, x1: x, y1: y }
  base = ev.ctrlKey || ev.metaKey ? new Set(props.selected) : new Set()
  if (!base.size) emit('select', new Set(), null)
  root.value!.setPointerCapture(ev.pointerId)
}
function onMove(ev: PointerEvent) {
  if (!box.value) return
  const r = root.value!.getBoundingClientRect()
  box.value.x1 = ev.clientX - r.left + root.value!.scrollLeft
  box.value.y1 = ev.clientY - r.top + root.value!.scrollTop
  const b = rect.value!
  const s = new Set(base)
  root.value!.querySelectorAll<HTMLElement>('.item').forEach((el) => {
    const ir = el.getBoundingClientRect()
    const x = ir.left - r.left + root.value!.scrollLeft
    const y = ir.top - r.top + root.value!.scrollTop
    if (x < b.left + b.width && x + ir.width > b.left && y < b.top + b.height && y + ir.height > b.top) s.add(el.dataset.path!)
  })
  emit('select', s, null)
}
function onUp() {
  box.value = null
}
const rect = computed(() => {
  const m = box.value
  if (!m) return null
  return { left: Math.min(m.x0, m.x1), top: Math.min(m.y0, m.y1), width: Math.abs(m.x1 - m.x0), height: Math.abs(m.y1 - m.y0) }
})

const cutSet = computed(() => new Set(props.cutPaths))
const arrow = (k: SortKey) => (props.sort.key === k ? (props.sort.desc ? '↓' : '↑') : '')
</script>

<template>
  <div
    ref="root"
    class="files sd-scroll"
    :class="view"
    data-testid="file-list"
    @pointerdown="onDown"
    @pointermove="onMove"
    @pointerup="onUp"
    @contextmenu="onMenu($event, null)"
    @dragover.prevent="emit('dragover', $event, null)"
    @drop.prevent="emit('drop', $event, null)"
  >
    <div v-if="view === 'details'" class="thead">
      <span class="th c-name" @click="emit('sort', 'name')">{{ t('explorer.name') }} {{ arrow('name') }}</span>
      <span class="th c-time" @click="emit('sort', 'mtime')">{{ t('explorer.modified') }} {{ arrow('mtime') }}</span>
      <span class="th c-type" @click="emit('sort', 'type')">{{ t('explorer.type') }} {{ arrow('type') }}</span>
      <span class="th c-size" @click="emit('sort', 'size')">{{ t('explorer.size') }} {{ arrow('size') }}</span>
      <span class="th c-perm">{{ t('explorer.permissions') }}</span>
      <span class="th c-owner">{{ t('explorer.owner') }}</span>
    </div>
    <div class="body">
      <div
        v-for="e in entries"
        :key="e.path"
        class="item"
        :class="{ sel: selected.has(e.path), cut: cutSet.has(e.path), drop: dropTarget === e.path, hidden: e.name.startsWith('.') }"
        :data-path="e.path"
        :data-testid="'file-' + e.name"
        draggable="true"
        :title="showPath ? e.path : undefined"
        @click.stop="click($event, e)"
        @dblclick.stop="emit('open', e)"
        @contextmenu="onMenu($event, e)"
        @dragstart="emit('dragstart', $event, e)"
        @dragover.prevent.stop="emit('dragover', $event, e)"
        @drop.prevent.stop="emit('drop', $event, e)"
      >
        <template v-if="view === 'details'">
          <span class="c-name">
            <SdIcon :name="fileKind(e.name, e.isDir)" :size="18" />
            <span class="sd-ellipsis">{{ e.name }}</span>
            <span v-if="e.type === 'link'" class="lnk" :title="e.linkTarget">↗</span>
            <span v-if="showPath" class="sd-muted sd-ellipsis path">{{ e.path }}</span>
          </span>
          <span class="c-time sd-muted">{{ formatTime(e.mtime) }}</span>
          <span class="c-type sd-muted sd-ellipsis">{{ typeLabel(e) }}</span>
          <span class="c-size sd-muted">{{ e.isDir ? '' : formatBytes(e.size) }}</span>
          <span class="c-perm sd-muted sd-mono">{{ e.mode }}</span>
          <span class="c-owner sd-muted sd-ellipsis">{{ e.owner }}:{{ e.group }}</span>
        </template>
        <template v-else-if="view === 'list'">
          <SdIcon :name="fileKind(e.name, e.isDir)" :size="18" />
          <span class="sd-ellipsis">{{ e.name }}</span>
        </template>
        <template v-else>
          <SdIcon :name="fileKind(e.name, e.isDir)" :size="56" />
          <span class="label">{{ e.name }}</span>
        </template>
      </div>
      <div v-if="!entries.length" class="empty sd-muted">{{ t('explorer.emptyFolder') }}</div>
    </div>
    <div v-if="rect" class="marquee" :style="{ left: rect.left + 'px', top: rect.top + 'px', width: rect.width + 'px', height: rect.height + 'px' }" />
  </div>
</template>

<style scoped>
.files {
  position: relative;
  height: 100%;
  user-select: none;
  outline: none;
}
.thead {
  position: sticky;
  top: 0;
  z-index: 1;
  display: flex;
  background: var(--sd-surface);
  border-bottom: 1px solid var(--sd-border);
  font-size: 12px;
  color: var(--sd-text-2);
}
.th {
  padding: 6px 8px;
  border-right: 1px solid var(--sd-border);
}
.th:hover {
  background: var(--sd-hover);
}
.c-name {
  flex: 1;
  min-width: 180px;
  display: flex;
  align-items: center;
  gap: 8px;
}
.c-time {
  width: 136px;
}
.c-type {
  width: 96px;
}
.c-size {
  width: 84px;
  text-align: right;
}
.c-perm {
  width: 96px;
}
.c-owner {
  width: 110px;
}
.details .item {
  display: flex;
  align-items: center;
  height: 30px;
  font-size: 13px;
}
.details .item > span {
  padding: 0 8px;
}
.item {
  border: 1px solid transparent;
  border-radius: 4px;
}
.item:hover {
  background: var(--sd-hover);
}
.item.sel {
  background: var(--sd-select);
  border-color: var(--sd-border-strong);
}
.item.drop {
  background: var(--sd-accent-soft);
  border-color: var(--sd-accent);
}
.item.cut,
.item.hidden {
  opacity: 0.55;
}
.lnk {
  color: var(--sd-accent);
  font-size: 11px;
}
.path {
  font-size: 11px;
  margin-left: 8px;
}
.list .body {
  display: flex;
  flex-direction: column;
  flex-wrap: wrap;
  align-content: flex-start;
  height: 100%;
  padding: 4px;
}
.list .item {
  width: 240px;
  height: 26px;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 0 6px;
  font-size: 13px;
}
.icons .body {
  display: grid;
  grid-template-columns: repeat(auto-fill, 104px);
  gap: 4px;
  padding: 8px;
  align-content: start;
}
.icons .item {
  height: 112px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 4px;
  padding: 6px 4px;
}
.label {
  font-size: 12px;
  text-align: center;
  word-break: break-all;
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.empty {
  padding: 40px;
  text-align: center;
  width: 100%;
}
.marquee {
  position: absolute;
  border: 1px solid var(--sd-accent);
  background: var(--sd-accent-soft);
  pointer-events: none;
}
</style>
