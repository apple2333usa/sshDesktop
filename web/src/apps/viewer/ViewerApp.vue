<script setup lang="ts">
// 预览：图片（缩放、旋转、同目录前后切换）、视频、音频、PDF。
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton } from 'naive-ui'
import { filesApi, triggerDownload } from '@/api/files'
import type { FileEntry } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { fileKind } from '@/components/icons'
import { parentOf } from '@/apps/explorer/fileops'

const host = useAppHost()
const { t } = useI18n()
const node = (host.props.node as string) || 'local'
const path = ref(host.props.path as string)
const siblings = ref<FileEntry[]>([])
const zoom = ref(0) // 0 = 适应窗口
const rotate = ref(0)
const failed = ref(false)
const natural = ref({ w: 0, h: 0 })

const name = computed(() => path.value.slice(path.value.lastIndexOf('/') + 1))
const kind = computed(() => fileKind(name.value, false))
const url = computed(() => filesApi.rawURL(node, path.value))

watch(
  path,
  (p) => {
    host.setTitle(name.value)
    host.setState({ path: p })
    zoom.value = 0
    rotate.value = 0
    failed.value = false
  },
  { immediate: true },
)

onMounted(async () => {
  if (kind.value !== 'image') return
  try {
    const r = await filesApi.list(node, parentOf(path.value))
    const coll = new Intl.Collator('zh-CN', { numeric: true })
    siblings.value = r.entries.filter((e) => !e.isDir && fileKind(e.name, false) === 'image').sort((a, b) => coll.compare(a.name, b.name))
  } catch {
    /* 没有目录权限时只显示当前图片 */
  }
})

const index = computed(() => siblings.value.findIndex((e) => e.path === path.value))
function step(d: number) {
  if (siblings.value.length < 2 || index.value < 0) return
  const n = siblings.value.length
  path.value = siblings.value[(index.value + d + n) % n].path
}

function onLoad(e: Event) {
  const img = e.target as HTMLImageElement
  natural.value = { w: img.naturalWidth, h: img.naturalHeight }
}

function setZoom(z: number) {
  zoom.value = Math.min(16, Math.max(0.05, z))
}
function onWheel(e: WheelEvent) {
  if (kind.value !== 'image') return
  e.preventDefault()
  const cur = zoom.value || fitScale()
  setZoom(cur * (e.deltaY < 0 ? 1.15 : 1 / 1.15))
}

const stage = ref<HTMLElement | null>(null)
function fitScale() {
  const s = stage.value
  if (!s || !natural.value.w) return 1
  const swap = rotate.value % 180 !== 0
  const w = swap ? natural.value.h : natural.value.w
  const h = swap ? natural.value.w : natural.value.h
  return Math.min(1, (s.clientWidth - 24) / w, (s.clientHeight - 24) / h)
}

const imgStyle = computed(() => {
  const z = zoom.value
  return {
    transform: `rotate(${rotate.value}deg)`,
    ...(z ? { width: natural.value.w * z + 'px', maxWidth: 'none', maxHeight: 'none' } : {}),
  }
})

const zoomLabel = computed(() => Math.round((zoom.value || fitScale()) * 100) + '%')

function onKey(e: KeyboardEvent) {
  if (kind.value !== 'image') return
  if (e.key === 'ArrowRight' || e.key === 'PageDown') step(1)
  else if (e.key === 'ArrowLeft' || e.key === 'PageUp') step(-1)
  else if (e.key === '+' || e.key === '=') setZoom((zoom.value || fitScale()) * 1.25)
  else if (e.key === '-') setZoom((zoom.value || fitScale()) / 1.25)
  else if (e.key === '0') zoom.value = 0
  else if (e.key.toLowerCase() === 'r') rotate.value = (rotate.value + 90) % 360
  else return
  e.preventDefault()
}

const root = ref<HTMLElement | null>(null)
onMounted(() => root.value?.focus())
</script>

<template>
  <div ref="root" class="viewer" tabindex="0" data-testid="viewer" @keydown="onKey">
    <div class="sd-toolbar">
      <template v-if="kind === 'image'">
        <NButton size="small" quaternary :disabled="siblings.length < 2" @click="step(-1)">‹ {{ t('viewer.prev') }}</NButton>
        <NButton size="small" quaternary :disabled="siblings.length < 2" @click="step(1)">{{ t('viewer.next') }} ›</NButton>
        <span class="sd-muted small">{{ index >= 0 ? `${index + 1} / ${siblings.length}` : '' }}</span>
        <NButton size="small" quaternary @click="setZoom((zoom || fitScale()) / 1.25)">－</NButton>
        <span class="small zoom">{{ zoomLabel }}</span>
        <NButton size="small" quaternary @click="setZoom((zoom || fitScale()) * 1.25)">＋</NButton>
        <NButton size="small" quaternary @click="zoom = 0">{{ t('viewer.fit') }}</NButton>
        <NButton size="small" quaternary @click="zoom = 1">1:1</NButton>
        <NButton size="small" quaternary @click="rotate = (rotate + 90) % 360">⟳ {{ t('viewer.rotate') }}</NButton>
      </template>
      <span class="sd-muted small sd-ellipsis grow">{{ path }}<template v-if="natural.w && kind === 'image'"> · {{ natural.w }}×{{ natural.h }}</template></span>
      <NButton size="small" quaternary @click="triggerDownload(filesApi.downloadURL(node, [path]))">{{ t('explorer.download') }}</NButton>
    </div>
    <div ref="stage" class="stage" :class="kind" @wheel="onWheel">
      <div v-if="failed" class="sd-muted">{{ t('viewer.failed') }}</div>
      <img v-else-if="kind === 'image'" :key="path" :src="url" :alt="name" :style="imgStyle" draggable="false" @load="onLoad" @error="failed = true" />
      <video v-else-if="kind === 'video'" :key="path" :src="url" controls autoplay @error="failed = true" />
      <audio v-else-if="kind === 'audio'" :key="path" :src="url" controls autoplay @error="failed = true" />
      <iframe v-else-if="kind === 'pdf'" :key="path" :src="url" :title="name" />
      <div v-else class="sd-muted">{{ t('viewer.unsupported') }}</div>
    </div>
  </div>
</template>

<style scoped>
.viewer {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--sd-surface);
  outline: none;
}
.small {
  font-size: 12px;
}
.zoom {
  min-width: 44px;
  text-align: center;
}
.grow {
  flex: 1;
  min-width: 0;
  text-align: right;
}
.stage {
  flex: 1;
  min-height: 0;
  overflow: auto;
  display: flex;
  align-items: center;
  justify-content: center;
  background: var(--sd-bg);
  border-top: 1px solid var(--sd-border);
}
.stage.image {
  background-color: var(--sd-bg);
  background-image: linear-gradient(45deg, var(--sd-hover) 25%, transparent 25%), linear-gradient(-45deg, var(--sd-hover) 25%, transparent 25%),
    linear-gradient(45deg, transparent 75%, var(--sd-hover) 75%), linear-gradient(-45deg, transparent 75%, var(--sd-hover) 75%);
  background-size: 20px 20px;
  background-position: 0 0, 0 10px, 10px -10px, -10px 0;
}
img {
  max-width: calc(100% - 24px);
  max-height: calc(100% - 24px);
  margin: auto;
  transition: transform 0.15s;
  image-rendering: auto;
}
video {
  max-width: 100%;
  max-height: 100%;
}
audio {
  width: min(480px, 90%);
}
iframe {
  width: 100%;
  height: 100%;
  border: 0;
  background: #fff;
}
</style>
