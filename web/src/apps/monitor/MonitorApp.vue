<script setup lang="ts">
// 监控概览：概要卡片 + 实时曲线（1 秒）+ 历史曲线（1 小时 / 1 天 / 7 天 / 30 天 / 自定义）。
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDatePicker, NProgress, NRadioButton, NRadioGroup, NSpin, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import { realtime } from '@/api/realtime'
import type { HostInfo, Metric, Sample } from '@/api/types'
import { useAppHost } from '@/apps/host'
import LineChart, { type ChartSeries } from '@/components/LineChart.vue'
import { errorText, formatBytes, formatDuration, formatRate } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const node = (host.props.node as string) || 'local'

const info = ref<HostInfo | null>(null)
const sample = ref<Sample | null>(null)
const points = shallowRef<Metric[]>([])
const mode = ref<'live' | '1h' | '24h' | '7d' | '30d' | 'custom'>((host.props.range as never) || 'live')
const custom = ref<[number, number] | null>(null)
const loading = ref(false)
const KEEP = 300

host.setTitle(t('apps.monitor'))

const pct = (a: number, b: number) => (b ? (a / b) * 100 : 0)
const fmtPct = (v: number) => `${v.toFixed(v < 10 ? 1 : 0)}%`
const fmtNum = (v: number) => (Number.isInteger(v) ? String(v) : v.toFixed(2))

/* ---------- 实时 ---------- */
let unsub: (() => void) | null = null
let pending: Sample[] = []

function onSample(s: Sample) {
  sample.value = s
  if (mode.value !== 'live') return
  pending.push(s)
  // 窗口不可见时只缓存，重新可见时一次性追加
  if (!host.visible.value) return
  flush()
}

function flush() {
  if (!pending.length) return
  const next = points.value.concat(pending)
  pending = []
  points.value = next.length > KEEP ? next.slice(next.length - KEEP) : next
}
watch(host.visible, (v) => v && flush())

async function loadLive() {
  loading.value = true
  try {
    points.value = await api.get<Sample[]>(`/nodes/${node}/monitor/realtime`, { seconds: KEEP })
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

/* ---------- 历史 ---------- */
const step = ref(1)
async function loadHistory() {
  const now = Math.floor(Date.now() / 1000)
  let from: number
  let to = now
  if (mode.value === 'custom') {
    if (!custom.value) return
    from = Math.floor(custom.value[0] / 1000)
    to = Math.floor(custom.value[1] / 1000)
  } else {
    from = now - { '1h': 3600, '24h': 86400, '7d': 7 * 86400, '30d': 30 * 86400 }[mode.value as '1h']
  }
  loading.value = true
  try {
    const r = await api.get<{ step: number; points: Metric[] }>(`/nodes/${node}/monitor/history`, { from, to })
    step.value = r.step
    points.value = r.points
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

function reload() {
  pending = []
  if (mode.value === 'live') loadLive()
  else loadHistory()
}

watch(mode, (m) => {
  host.setState({ range: m })
  reload()
})
watch(custom, () => mode.value === 'custom' && loadHistory())

onMounted(async () => {
  try {
    const r = await api.get<{ info: HostInfo; sample: Sample | null }>(`/nodes/${node}/monitor/current`)
    info.value = r.info
    sample.value = r.sample
  } catch (e) {
    message.error(errorText(e))
  }
  reload()
  unsub = realtime.subscribe(`metrics:${node}`, onSample)
})
onUnmounted(() => unsub?.())

/* ---------- 图表数据 ---------- */
const long = computed(() => mode.value !== 'live' && mode.value !== '1h')
// 历史数据有缺口时插入空值，让曲线断开而不是连成斜线
function series(name: string, pick: (m: Metric) => number): ChartSeries {
  const data: [number, number][] = []
  const gap = mode.value === 'live' ? 5 : step.value * 2.5
  let prev = 0
  for (const m of points.value) {
    if (prev && m.ts - prev > gap) data.push([(prev + 1) * 1000, NaN])
    data.push([m.ts * 1000, pick(m)])
    prev = m.ts
  }
  return { name, data }
}

const cpu = computed(() => [series(t('monitor.cpu'), (m) => m.cpu)])
const mem = computed(() => [series(t('monitor.memory'), (m) => pct(m.memUsed, m.memTotal)), series(t('monitor.swap'), (m) => pct(m.swapUsed, m.swapTotal))])
const load = computed(() => [series('1 min', (m) => m.load1), series('5 min', (m) => m.load5), series('15 min', (m) => m.load15)])
const net = computed(() => [series(t('monitor.rx'), (m) => m.netRxBps), series(t('monitor.tx'), (m) => m.netTxBps)])
const disk = computed(() => [series(t('monitor.read'), (m) => m.diskReadBps), series(t('monitor.write'), (m) => m.diskWriteBps)])
const conns = computed(() => [series(t('monitor.tcp'), (m) => m.tcpConns), series(t('monitor.procs'), (m) => m.procs)])

const s = computed(() => sample.value)
const memPct = computed(() => (s.value ? pct(s.value.memUsed, s.value.memTotal) : 0))
const swapPct = computed(() => (s.value ? pct(s.value.swapUsed, s.value.swapTotal) : 0))
const diskPct = computed(() => (s.value ? pct(s.value.diskUsed, s.value.diskTotal) : 0))
const status = (v: number) => (v > 90 ? 'error' : v > 75 ? 'warning' : 'info')
</script>

<template>
  <div class="monitor sd-scroll" data-testid="monitor">
    <div class="head">
      <div v-if="info" class="hostline">
        <b>{{ info.hostname }}</b>
        <span class="sd-muted">{{ info.platform }} {{ info.platformVersion }} · {{ info.kernel }} · {{ info.arch }}</span>
        <span class="sd-muted">{{ info.cpuModel }} · {{ t('monitor.cores', { c: info.cpuCores, t: info.cpuThreads }) }}</span>
        <span v-if="s" class="sd-muted">{{ t('monitor.uptime') }} {{ formatDuration(s.uptime) }}</span>
      </div>
      <div class="range">
        <NRadioGroup v-model:value="mode" size="small">
          <NRadioButton value="live" data-testid="monitor-live">{{ t('monitor.live') }}</NRadioButton>
          <NRadioButton value="1h">{{ t('monitor.h1') }}</NRadioButton>
          <NRadioButton value="24h">{{ t('monitor.d1') }}</NRadioButton>
          <NRadioButton value="7d">{{ t('monitor.d7') }}</NRadioButton>
          <NRadioButton value="30d">{{ t('monitor.d30') }}</NRadioButton>
          <NRadioButton value="custom">{{ t('monitor.custom') }}</NRadioButton>
        </NRadioGroup>
        <NDatePicker v-if="mode === 'custom'" v-model:value="custom" type="datetimerange" size="small" clearable />
        <NButton v-if="mode !== 'live'" size="small" quaternary @click="reload">{{ t('explorer.refresh') }}</NButton>
      </div>
    </div>

    <div v-if="s" class="cards">
      <div class="card" data-testid="card-cpu">
        <div class="label">{{ t('monitor.cpu') }}</div>
        <div class="value">{{ fmtPct(s.cpu) }}</div>
        <NProgress type="line" :percentage="Math.min(100, s.cpu)" :status="status(s.cpu)" :show-indicator="false" :height="6" />
        <div class="sub sd-muted">{{ t('monitor.load') }} {{ fmtNum(s.load1) }} / {{ fmtNum(s.load5) }} / {{ fmtNum(s.load15) }}</div>
      </div>
      <div class="card">
        <div class="label">{{ t('monitor.memory') }}</div>
        <div class="value">{{ fmtPct(memPct) }}</div>
        <NProgress type="line" :percentage="memPct" :status="status(memPct)" :show-indicator="false" :height="6" />
        <div class="sub sd-muted">{{ formatBytes(s.memUsed) }} / {{ formatBytes(s.memTotal) }}</div>
      </div>
      <div class="card">
        <div class="label">{{ t('monitor.swap') }}</div>
        <div class="value">{{ s.swapTotal ? fmtPct(swapPct) : '-' }}</div>
        <NProgress type="line" :percentage="swapPct" :status="status(swapPct)" :show-indicator="false" :height="6" />
        <div class="sub sd-muted">{{ s.swapTotal ? `${formatBytes(s.swapUsed)} / ${formatBytes(s.swapTotal)}` : t('monitor.noSwap') }}</div>
      </div>
      <div class="card">
        <div class="label">{{ t('monitor.disk') }}</div>
        <div class="value">{{ fmtPct(diskPct) }}</div>
        <NProgress type="line" :percentage="diskPct" :status="status(diskPct)" :show-indicator="false" :height="6" />
        <div class="sub sd-muted">{{ formatBytes(s.diskUsed) }} / {{ formatBytes(s.diskTotal) }}</div>
      </div>
      <div class="card">
        <div class="label">{{ t('monitor.network') }}</div>
        <div class="value small">↓ {{ formatRate(s.netRxBps) }}</div>
        <div class="value small">↑ {{ formatRate(s.netTxBps) }}</div>
      </div>
      <div class="card">
        <div class="label">{{ t('monitor.diskIO') }}</div>
        <div class="value small">R {{ formatRate(s.diskReadBps) }}</div>
        <div class="value small">W {{ formatRate(s.diskWriteBps) }}</div>
      </div>
      <div class="card">
        <div class="label">{{ t('monitor.tcp') }} / {{ t('monitor.procs') }}</div>
        <div class="value">{{ s.tcpConns }} <span class="sd-muted">/ {{ s.procs }}</span></div>
      </div>
    </div>

    <NSpin :show="loading">
      <div class="charts">
        <section>
          <h4>{{ t('monitor.cpu') }} (%)</h4>
          <div class="plot"><LineChart :series="cpu" :format="fmtPct" :max="100" :long="long" area /></div>
        </section>
        <section>
          <h4>{{ t('monitor.memory') }} / {{ t('monitor.swap') }} (%)</h4>
          <div class="plot"><LineChart :series="mem" :format="fmtPct" :max="100" :long="long" /></div>
        </section>
        <section>
          <h4>{{ t('monitor.network') }}</h4>
          <div class="plot"><LineChart :series="net" :format="formatRate" :long="long" /></div>
        </section>
        <section>
          <h4>{{ t('monitor.diskIO') }}</h4>
          <div class="plot"><LineChart :series="disk" :format="formatRate" :long="long" /></div>
        </section>
        <section>
          <h4>{{ t('monitor.load') }}</h4>
          <div class="plot"><LineChart :series="load" :format="fmtNum" :long="long" /></div>
        </section>
        <section>
          <h4>{{ t('monitor.tcp') }} / {{ t('monitor.procs') }}</h4>
          <div class="plot"><LineChart :series="conns" :format="(v) => String(Math.round(v))" :long="long" /></div>
        </section>
      </div>
    </NSpin>

    <div v-if="s?.cpuCores?.length" class="block">
      <h4>{{ t('monitor.perCore') }}</h4>
      <div class="cores">
        <div v-for="(c, i) in s.cpuCores" :key="i" class="core" :title="`CPU ${i}: ${fmtPct(c)}`">
          <div class="bar"><div class="fill" :style="{ height: Math.min(100, c) + '%' }" /></div>
          <span class="sd-muted">{{ i }}</span>
        </div>
      </div>
    </div>

    <div v-if="s?.disks?.length" class="block">
      <h4>{{ t('monitor.disks') }}</h4>
      <table class="disks">
        <thead>
          <tr>
            <th>{{ t('monitor.mount') }}</th>
            <th>{{ t('monitor.device') }}</th>
            <th>{{ t('monitor.fsType') }}</th>
            <th class="w">{{ t('monitor.usage') }}</th>
            <th class="r">{{ t('monitor.usedTotal') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in s.disks" :key="d.mount">
            <td class="sd-mono">{{ d.mount }}</td>
            <td class="sd-muted sd-mono">{{ d.device }}</td>
            <td class="sd-muted">{{ d.fsType }}</td>
            <td><NProgress type="line" :percentage="Math.round(pct(d.used, d.total))" :status="status(pct(d.used, d.total))" :height="8" /></td>
            <td class="r sd-muted">{{ formatBytes(d.used) }} / {{ formatBytes(d.total) }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.monitor {
  height: 100%;
  padding: 12px 16px 20px;
  background: var(--sd-surface);
}
.head {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.hostline {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  font-size: 13px;
  align-items: baseline;
}
.range {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: 10px;
  margin-bottom: 14px;
}
.card {
  padding: 10px 12px;
  border-radius: var(--sd-radius);
  background: var(--sd-surface-2);
  border: 1px solid var(--sd-border);
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.label {
  font-size: 12px;
  color: var(--sd-text-2);
}
.value {
  font-size: 22px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.value.small {
  font-size: 14px;
}
.sub {
  font-size: 12px;
}
.charts {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(380px, 1fr));
  gap: 10px;
}
section,
.block {
  padding: 10px 12px;
  border-radius: var(--sd-radius);
  background: var(--sd-surface-2);
  border: 1px solid var(--sd-border);
}
.block {
  margin-top: 10px;
}
h4 {
  margin: 0 0 6px;
  font-size: 13px;
  font-weight: 600;
}
.plot {
  height: 180px;
}
.cores {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}
.core {
  display: flex;
  flex-direction: column;
  align-items: center;
  font-size: 10px;
  gap: 2px;
}
.bar {
  width: 14px;
  height: 48px;
  border-radius: 3px;
  background: var(--sd-active);
  display: flex;
  align-items: flex-end;
  overflow: hidden;
}
.fill {
  width: 100%;
  background: var(--sd-accent);
  border-radius: 3px 3px 0 0;
}
.disks {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.disks th {
  text-align: left;
  font-weight: normal;
  color: var(--sd-text-2);
  font-size: 12px;
  padding: 4px 8px;
}
.disks td {
  padding: 5px 8px;
  border-top: 1px solid var(--sd-border);
}
.disks .w {
  width: 30%;
}
.r {
  text-align: right;
  white-space: nowrap;
}
@media (max-width: 640px) {
  .charts {
    grid-template-columns: 1fr;
  }
}
</style>
