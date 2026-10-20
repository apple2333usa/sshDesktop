<script setup lang="ts">
// 通知中心：后台任务进度和结果、上传进度、各类提示；下方为日历。
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NProgress } from 'naive-ui'
import { useNotify, type Notice } from '@/stores/notify'
import { formatTime } from '@/i18n'

const { t } = useI18n()
const notify = useNotify()

onMounted(() => notify.markAllRead())

const today = new Date()
const days = computed(() => {
  const first = new Date(today.getFullYear(), today.getMonth(), 1)
  const start = (first.getDay() + 6) % 7 // 周一开头
  const n = new Date(today.getFullYear(), today.getMonth() + 1, 0).getDate()
  const cells: (number | null)[] = Array(start).fill(null)
  for (let i = 1; i <= n; i++) cells.push(i)
  return cells
})

function color(n: Notice) {
  return { success: 'var(--sd-success)', error: 'var(--sd-danger)', warning: 'var(--sd-warning)', info: 'var(--sd-accent)' }[n.type]
}
</script>

<template>
  <div class="nc" data-testid="notify-center" @click.stop>
    <div class="panel list">
      <div class="head">
        <span class="h">{{ t('notify.title') }}</span>
        <NButton size="tiny" quaternary @click="notify.clear()">{{ t('notify.clearAll') }}</NButton>
      </div>
      <div class="items sd-scroll">
        <div v-if="!notify.list.length" class="empty sd-muted">{{ t('notify.empty') }}</div>
        <div v-for="n in notify.list" :key="n.id" class="item" :style="{ borderLeftColor: color(n) }">
          <div class="row">
            <span class="title sd-ellipsis">{{ n.title }}</span>
            <span class="time sd-muted">{{ formatTime(n.time).slice(11) }}</span>
            <button class="x" :title="t('common.close')" @click="notify.remove(n.id)">×</button>
          </div>
          <div v-if="n.content" class="content">{{ n.content }}</div>
          <div v-if="n.progress !== undefined && !n.done" class="prog">
            <NProgress type="line" :percentage="Math.round(n.progress)" :height="4" :show-indicator="false" />
            <NButton v-if="n.cancel" size="tiny" quaternary @click="n.cancel()">{{ t('common.cancel') }}</NButton>
          </div>
        </div>
      </div>
    </div>
    <div class="panel cal">
      <div class="h">{{ today.toLocaleDateString('zh-CN', { year: 'numeric', month: 'long', day: 'numeric', weekday: 'long' }) }}</div>
      <div class="grid">
        <span v-for="w in ['一', '二', '三', '四', '五', '六', '日']" :key="w" class="wd sd-muted">{{ w }}</span>
        <span v-for="(d, i) in days" :key="i" class="d" :class="{ today: d === today.getDate() }">{{ d ?? '' }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.nc {
  position: absolute;
  right: 12px;
  bottom: 60px;
  width: min(360px, calc(100vw - 24px));
  max-height: calc(100vh - 80px);
  display: flex;
  flex-direction: column;
  gap: 12px;
  z-index: 100001;
  animation: slide 0.18s ease-out;
}
@keyframes slide {
  from {
    transform: translateX(40px);
    opacity: 0;
  }
}
.panel {
  background: var(--sd-acrylic);
  backdrop-filter: blur(40px) saturate(150%);
  border: 1px solid var(--sd-border-strong);
  border-radius: 8px;
  box-shadow: var(--sd-shadow);
  padding: 12px 16px;
}
.list {
  min-height: 0;
  flex: 1;
  display: flex;
  flex-direction: column;
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 8px;
}
.h {
  font-weight: 600;
}
.items {
  max-height: 46vh;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.empty {
  padding: 24px 0;
  text-align: center;
}
.item {
  background: var(--sd-surface);
  border-radius: 6px;
  border-left: 3px solid;
  padding: 8px 10px;
}
.row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.title {
  flex: 1;
  font-weight: 600;
  font-size: 13px;
}
.time {
  font-size: 12px;
}
.x {
  border: none;
  background: none;
  color: var(--sd-text-2);
  font-size: 16px;
  line-height: 1;
  padding: 0 2px;
}
.content {
  font-size: 12px;
  margin-top: 4px;
  word-break: break-all;
  color: var(--sd-text-2);
}
.prog {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 6px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(7, 1fr);
  gap: 4px;
  margin-top: 10px;
  text-align: center;
  font-size: 13px;
}
.d {
  padding: 6px 0;
  border-radius: 50%;
}
.d.today {
  background: var(--sd-accent);
  color: #fff;
}
:root[data-theme='dark'] .d.today {
  color: #000;
}
</style>
