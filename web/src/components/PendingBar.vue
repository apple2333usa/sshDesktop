<script setup lang="ts">
// 等待确认的修改（防火墙、SSH 设置）：倒计时结束前不确认，服务器会自动回滚。
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { PendingChange } from '@/api/types'
import { errorText } from '@/i18n'

const props = defineProps<{ change: PendingChange; base: string; canManage: boolean }>()
const emit = defineEmits<{ done: [] }>()
const { t } = useI18n()
const message = useMessage()

const now = ref(Date.now())
const timer = window.setInterval(() => (now.value = Date.now()), 500)
onBeforeUnmount(() => clearInterval(timer))
const left = computed(() => Math.max(0, Math.ceil((Date.parse(props.change.deadline) - now.value) / 1000)))
// 到时服务器会回滚，稍等后刷新
watch(left, (v) => v === 0 && setTimeout(() => emit('done'), 2500))

const busy = ref(false)
async function act(what: 'confirm' | 'revert') {
  busy.value = true
  try {
    await api.post(`${props.base}/pending/${what}`, { id: props.change.id })
    message.success(what === 'confirm' ? t('pending.kept') : t('pending.reverted'))
    setTimeout(() => emit('done'), what === 'revert' ? 1500 : 0)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <NAlert type="warning" :title="t('pending.title', { title: change.title })" class="pending" data-testid="pending-bar">
    <ul class="detail">
      <li v-for="d in change.detail" :key="d">{{ d }}</li>
    </ul>
    <div class="row">
      <span>{{ left > 0 ? t('pending.left', { n: left }) : t('pending.rollingBack') }}</span>
      <span class="sd-muted small">{{ t('pending.by', { user: change.username }) }}</span>
      <span style="flex: 1" />
      <template v-if="canManage && left > 0">
        <NButton size="small" :disabled="busy" data-testid="pending-revert" @click="act('revert')">{{ t('pending.revert') }}</NButton>
        <NButton size="small" type="primary" :loading="busy" data-testid="pending-confirm" @click="act('confirm')">{{ t('pending.keep') }}</NButton>
      </template>
    </div>
  </NAlert>
</template>

<style scoped>
.pending {
  margin: 8px 10px 0;
}
.detail {
  margin: 2px 0 6px;
  padding-left: 18px;
}
.row {
  display: flex;
  gap: 8px;
  align-items: center;
}
.small {
  font-size: 12px;
}
</style>
