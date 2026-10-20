<script setup lang="ts">
// 小组件面板：本机 CPU、内存、磁盘、网速、运行时间，以及各服务器的在线状态。
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NProgress } from 'naive-ui'
import { realtime } from '@/api/realtime'
import { useHost } from '@/stores/host'
import { useSession } from '@/stores/session'
import { useNodes } from '@/stores/nodes'
import { formatBytes, formatDuration, formatRate } from '@/i18n'
import type { Sample } from '@/api/types'

const emit = defineEmits<{ open: [string] }>()
const { t } = useI18n()
const host = useHost()
const session = useSession()
const nodes = useNodes()
const s = ref<Sample | null>(null)
let unsub: (() => void) | null = null

onMounted(() => {
  if (session.has('monitor:view')) unsub = realtime.subscribe('metrics:local', (x: Sample) => (s.value = x))
})
onUnmounted(() => unsub?.())

const mem = computed(() => (s.value && s.value.memTotal ? (s.value.memUsed / s.value.memTotal) * 100 : 0))
const disk = computed(() => (s.value && s.value.diskTotal ? (s.value.diskUsed / s.value.diskTotal) * 100 : 0))
const status = (v: number) => (v > 90 ? 'error' : v > 75 ? 'warning' : 'success')
</script>

<template>
  <div class="widgets" data-testid="widgets" @click.stop>
    <div class="hello">
      <div class="h">{{ host.info?.host.hostname ?? '' }}</div>
      <div class="sd-muted">{{ host.info?.host.platform }} {{ host.info?.host.platformVersion }} · {{ host.info?.host.arch }}</div>
    </div>
    <div v-if="!session.has('monitor:view')" class="card sd-muted">{{ t('widgets.noPerm') }}</div>
    <template v-else-if="s">
      <div class="cards">
        <div class="card" @click="emit('open', 'monitor')">
          <div class="k">CPU</div>
          <NProgress type="circle" :percentage="Math.round(s.cpu)" :status="status(s.cpu)" :stroke-width="10" style="width: 84px" />
          <div class="sd-muted small">{{ t('widgets.load') }} {{ s.load1.toFixed(2) }}</div>
        </div>
        <div class="card" @click="emit('open', 'monitor')">
          <div class="k">{{ t('widgets.memory') }}</div>
          <NProgress type="circle" :percentage="Math.round(mem)" :status="status(mem)" :stroke-width="10" style="width: 84px" />
          <div class="sd-muted small">{{ formatBytes(s.memUsed) }} / {{ formatBytes(s.memTotal) }}</div>
        </div>
      </div>
      <div class="card wide" @click="emit('open', 'explorer')">
        <div class="k">{{ t('widgets.disk') }}</div>
        <NProgress type="line" :percentage="Math.round(disk)" :status="status(disk)" />
        <div class="sd-muted small">{{ formatBytes(s.diskUsed) }} / {{ formatBytes(s.diskTotal) }}</div>
      </div>
      <div class="card wide">
        <div class="k">{{ t('widgets.network') }}</div>
        <div class="net">
          <span>↑ {{ formatRate(s.netTxBps) }}</span>
          <span>↓ {{ formatRate(s.netRxBps) }}</span>
        </div>
      </div>
      <div class="card wide">
        <div class="k">{{ t('widgets.uptime') }}</div>
        <div>{{ formatDuration(s.uptime) }}</div>
      </div>
    </template>
    <div v-else class="card sd-muted">{{ t('common.loading') }}</div>
    <div v-if="nodes.remote.length" class="card wide" data-testid="widget-nodes" @click="emit('open', 'servers')">
      <div class="k">{{ t('widgets.nodes', { online: nodes.onlineCount, total: nodes.list.length }) }}</div>
      <div v-for="n in nodes.remote" :key="n.id" class="node">
        <i class="dot" :class="n.status.state" />
        <span class="sd-ellipsis">{{ n.name }}</span>
        <span class="sd-muted small">{{ t('servers.state.' + n.status.state) }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.widgets {
  position: absolute;
  left: 12px;
  top: 12px;
  bottom: 60px;
  width: min(380px, calc(100vw - 24px));
  padding: 20px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  background: var(--sd-acrylic);
  backdrop-filter: blur(40px) saturate(150%);
  border: 1px solid var(--sd-border-strong);
  border-radius: 8px;
  box-shadow: var(--sd-shadow);
  z-index: 100001;
  overflow: auto;
  animation: slide 0.18s ease-out;
}
@keyframes slide {
  from {
    transform: translateX(-40px);
    opacity: 0;
  }
}
.h {
  font-size: 20px;
  font-weight: 600;
}
.cards {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}
.card {
  background: var(--sd-surface);
  border-radius: 8px;
  padding: 14px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--sd-border);
}
.card.wide {
  align-items: stretch;
}
.k {
  font-weight: 600;
  align-self: flex-start;
}
.small {
  font-size: 12px;
}
.net {
  display: flex;
  justify-content: space-between;
  font-family: var(--sd-mono);
}
.node {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 13px;
  padding: 2px 0;
}
.node .sd-ellipsis {
  flex: 1;
  min-width: 0;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
  background: #8a8a8a;
}
.dot.online {
  background: #16a34a;
}
.dot.connecting {
  background: #d97706;
}
.dot.hostkey-mismatch,
.dot.auth-failed {
  background: #dc2626;
}
</style>
