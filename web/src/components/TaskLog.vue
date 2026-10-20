<script setup lang="ts">
// 后台任务的实时日志窗口：先取已有日志，再订阅 task:<id> 推送的新行和状态。
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NModal, NProgress, NTag } from 'naive-ui'
import { api } from '@/api/http'
import { realtime } from '@/api/realtime'
import type { Task } from '@/api/types'
import { useNotify } from '@/stores/notify'

const props = defineProps<{ task: Task | null }>()
const emit = defineEmits<{ 'update:task': [Task | null] }>()
const { t } = useI18n()
const notify = useNotify()

const cur = ref<Task | null>(null)
const lines = ref<string[]>([])
const box = ref<HTMLElement | null>(null)
let unsub: (() => void) | null = null

function scroll() {
  nextTick(() => box.value && (box.value.scrollTop = box.value.scrollHeight))
}

async function open(task: Task) {
  unsub?.()
  cur.value = task
  lines.value = []
  unsub = realtime.subscribe('task:' + task.id, (ev: { task: Task; log?: string }) => {
    cur.value = ev.task
    if (ev.log !== undefined) {
      lines.value.push(ev.log)
      scroll()
    }
  })
  try {
    const r = await api.get<{ task: Task; logs: { line: string }[] }>(`/tasks/${task.id}`)
    // 订阅前的日志放在前面（订阅后收到的行已追加在 lines 中）
    lines.value = r.logs.map((l) => l.line).concat(lines.value.filter((l) => !r.logs.some((x) => x.line === l)))
    if (r.task.status !== 'running') cur.value = r.task
    scroll()
  } catch {
    /* 忽略 */
  }
}

watch(
  () => props.task,
  (v) => (v ? open(v) : (unsub?.(), (unsub = null))),
  { immediate: true },
)
onUnmounted(() => unsub?.())

const show = computed({
  get: () => props.task !== null,
  set: (v) => !v && emit('update:task', null),
})
const statusType = computed(() =>
  ({ running: 'info', success: 'success', failed: 'error', canceled: 'warning' } as const)[cur.value?.status ?? 'running'],
)
</script>

<template>
  <NModal v-model:show="show" preset="card" :title="cur?.title" style="width: 760px; max-width: calc(100vw - 32px)" data-testid="task-log">
    <div class="head">
      <NTag size="small" :type="statusType" :bordered="false" data-testid="task-status">{{ t('task.' + (cur?.status ?? 'running')) }}</NTag>
      <NProgress v-if="cur?.status === 'running'" type="line" :percentage="Math.round(cur.progress)" :show-indicator="false" style="flex: 1" />
      <span v-else-if="cur?.error" class="err">{{ cur.error }}</span>
    </div>
    <pre ref="box" class="log sd-mono">{{ lines.join('\n') || t('task.noLog') }}</pre>
    <template #footer>
      <div class="foot">
        <NButton v-if="cur?.status === 'running'" size="small" @click="notify.cancelTask(cur.id)">{{ t('task.cancel') }}</NButton>
        <span style="flex: 1" />
        <NButton size="small" @click="show = false">{{ cur?.status === 'running' ? t('task.background') : t('common.close') }}</NButton>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.head {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-bottom: 8px;
}
.err {
  color: var(--sd-danger, #dc2626);
  font-size: 13px;
  word-break: break-all;
}
.log {
  height: 360px;
  max-height: 60vh;
  overflow: auto;
  margin: 0;
  padding: 8px;
  font-size: 12px;
  line-height: 1.5;
  border-radius: 4px;
  background: var(--sd-bg);
  white-space: pre-wrap;
  word-break: break-all;
}
.foot {
  display: flex;
  gap: 8px;
}
</style>
