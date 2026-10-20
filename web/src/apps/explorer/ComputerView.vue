<script setup lang="ts">
// “此电脑”：本机的挂载点，显示容量和使用情况。
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NProgress, NSpin } from 'naive-ui'
import { filesApi } from '@/api/files'
import type { MountInfo } from '@/api/types'
import { formatBytes } from '@/i18n'
import SdIcon from '@/components/SdIcon.vue'

const props = defineProps<{ node: string }>()
const emit = defineEmits<{ open: [string] }>()
const { t } = useI18n()
const mounts = ref<MountInfo[]>([])
const loading = ref(true)

onMounted(async () => {
  try {
    mounts.value = await filesApi.mounts(props.node)
  } finally {
    loading.value = false
  }
})

const pct = (m: MountInfo) => (m.total ? Math.round((m.used / m.total) * 100) : 0)
const status = (m: MountInfo) => (pct(m) > 90 ? 'error' : pct(m) > 75 ? 'warning' : 'info')
</script>

<template>
  <NSpin :show="loading" class="wrap">
    <div class="sec">{{ t('explorer.devices') }}</div>
    <div class="grid">
      <div v-for="m in mounts" :key="m.point" class="drive" :data-testid="'mount-' + m.point" @dblclick="emit('open', m.point)">
        <SdIcon :name="m.point === '/' ? 'diskSystem' : 'disk'" :size="44" />
        <div class="info">
          <div class="name sd-ellipsis">{{ m.point === '/' ? t('explorer.systemDisk') : m.point }} <span class="sd-muted">({{ m.fsType }})</span></div>
          <NProgress type="line" :percentage="pct(m)" :status="status(m)" :show-indicator="false" :height="12" :border-radius="2" />
          <div class="sd-muted small">{{ t('explorer.freeOf', { free: formatBytes(m.free), total: formatBytes(m.total) }) }}</div>
        </div>
      </div>
    </div>
  </NSpin>
</template>

<style scoped>
.wrap {
  padding: 12px 16px;
}
.sec {
  font-weight: 600;
  margin: 4px 0 10px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  gap: 8px;
}
.drive {
  display: flex;
  gap: 12px;
  align-items: center;
  padding: 10px;
  border-radius: 4px;
  border: 1px solid transparent;
}
.drive:hover {
  background: var(--sd-hover);
  border-color: var(--sd-border);
}
.info {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.small {
  font-size: 12px;
}
</style>
