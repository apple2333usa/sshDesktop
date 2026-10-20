<script setup lang="ts">
// 经 WebSocket 实时显示日志（容器、Compose 项目、应用）。服务端每次推送一个字符串数组。
import { nextTick, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NInput, NSwitch } from 'naive-ui'
import { wsURL } from '@/api/http'

const props = defineProps<{ path: string | null; active?: boolean }>()
const { t } = useI18n()
const lines = ref<string[]>([])
const follow = ref(true)
const filter = ref('')
const state = ref<'connecting' | 'open' | 'closed'>('closed')
const box = ref<HTMLElement | null>(null)
let ws: WebSocket | null = null

function close() {
  ws?.close()
  ws = null
  state.value = 'closed'
}

function open() {
  close()
  lines.value = []
  if (!props.path) return
  state.value = 'connecting'
  const sock = new WebSocket(wsURL(props.path))
  ws = sock
  sock.onopen = () => (state.value = 'open')
  sock.onclose = () => ws === sock && (state.value = 'closed')
  sock.onmessage = (ev) => {
    try {
      const next = lines.value.concat(JSON.parse(ev.data) as string[])
      lines.value = next.length > 5000 ? next.slice(next.length - 5000) : next
      if (follow.value) nextTick(() => box.value && (box.value.scrollTop = box.value.scrollHeight))
    } catch {
      /* 忽略 */
    }
  }
}

watch(
  () => [props.path, props.active !== false] as const,
  ([p, on]) => (p && on ? open() : close()),
  { immediate: true },
)
onUnmounted(close)

const shown = () => {
  const f = filter.value.trim().toLowerCase()
  return (f ? lines.value.filter((l) => l.toLowerCase().includes(f)) : lines.value).join('\n')
}
</script>

<template>
  <div class="logstream">
    <div class="bar">
      <NInput v-model:value="filter" size="tiny" clearable :placeholder="t('docker.logFilter')" style="max-width: 200px" />
      <span class="opt">{{ t('services.follow') }} <NSwitch v-model:value="follow" size="small" /></span>
      <span class="sd-muted small">{{ state === 'open' ? t('docker.logLive') : state === 'connecting' ? t('common.loading') : t('docker.logClosed') }}</span>
      <span style="flex: 1" />
      <NButton size="tiny" quaternary @click="open">{{ t('explorer.refresh') }}</NButton>
    </div>
    <pre ref="box" class="logs sd-mono" data-testid="log-stream">{{ shown() }}</pre>
  </div>
</template>

<style scoped>
.logstream {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.bar {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 6px;
}
.opt {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  font-size: 12px;
}
.small {
  font-size: 12px;
}
.logs {
  flex: 1;
  min-height: 160px;
  margin: 0;
  overflow: auto;
  font-size: 12px;
  line-height: 1.5;
  padding: 8px;
  border-radius: 4px;
  background: var(--sd-bg);
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
