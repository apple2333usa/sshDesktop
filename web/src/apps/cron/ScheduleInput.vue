<script setup lang="ts">
// cron 表达式输入：常用周期快捷选择，输入时预览接下来几次执行时间。
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NDropdown, NInput, NButton } from 'naive-ui'
import { api } from '@/api/http'
import { formatTime } from '@/i18n'

const props = defineProps<{ value: string }>()
const emit = defineEmits<{ 'update:value': [string] }>()
const { t } = useI18n()

const presets: [string, string][] = [
  ['every5m', '*/5 * * * *'],
  ['hourly', '0 * * * *'],
  ['daily', '0 3 * * *'],
  ['weekly', '0 3 * * 0'],
  ['monthly', '0 3 1 * *'],
  ['reboot', '@reboot'],
]
const next = ref<number[]>([])
const reboot = ref(false)
const error = ref('')
let timer: number | undefined

watch(
  () => props.value,
  (v) => {
    clearTimeout(timer)
    timer = window.setTimeout(async () => {
      if (!v.trim()) {
        next.value = []
        error.value = ''
        return
      }
      try {
        const r = await api.post<{ next: number[]; reboot: boolean }>('/nodes/local/cron/preview', { schedule: v })
        next.value = r.next
        reboot.value = r.reboot
        error.value = ''
      } catch (e) {
        next.value = []
        error.value = (e as Error).message
      }
    }, 300)
  },
  { immediate: true },
)
</script>

<template>
  <div class="sched">
    <div class="row">
      <NInput :value="props.value" class="sd-mono" placeholder="0 3 * * *" data-testid="cron-schedule" @update:value="(v: string) => emit('update:value', v)" />
      <NDropdown trigger="click" :options="presets.map(([k, v]) => ({ label: t('cron.preset.' + k) + '　' + v, key: v }))" @select="(k: string) => emit('update:value', k)">
        <NButton size="small">{{ t('cron.presets') }}</NButton>
      </NDropdown>
    </div>
    <div class="hint small">
      <span v-if="error" class="err">{{ error }}</span>
      <span v-else-if="reboot" class="sd-muted">{{ t('cron.rebootHint') }}</span>
      <span v-else-if="next.length" class="sd-muted">{{ t('cron.nextRuns') }}：{{ next.slice(0, 3).map((n) => formatTime(n * 1000)).join('，') }}</span>
      <span v-else class="sd-muted">{{ t('cron.scheduleHint') }}</span>
    </div>
  </div>
</template>

<style scoped>
.sched {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.row {
  display: flex;
  gap: 6px;
}
.small {
  font-size: 12px;
}
.err {
  color: var(--sd-danger);
}
</style>
