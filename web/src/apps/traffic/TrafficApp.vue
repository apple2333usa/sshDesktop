<script setup lang="ts">
// 按月流量：各节点本期用量总表；选中节点后看每日柱状图、各网卡合计、最近 12 期，并设置重置日与额度。
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NForm, NFormItem, NInputNumber, NModal, NProgress, NRadioButton, NRadioGroup, NSelect, NSpin, useMessage,
} from 'naive-ui'
import { api } from '@/api/http'
import type { TrafficDetail, TrafficUsage } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useNodes } from '@/stores/nodes'
import { useSession } from '@/stores/session'
import BarChart from '@/components/BarChart.vue'
import { errorText, formatBytes } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const nodes = useNodes()
const session = useSession()

const summary = ref<TrafficUsage[]>([])
const selected = ref<string>((host.props.node as string) || 'local')
const cycle = ref(0)
const detail = ref<TrafficDetail | null>(null)
const loading = ref(false)
const canManage = computed(() => session.has('nodes:manage'))

const day = (d: number) => `${Math.floor(d / 10000)}-${String(Math.floor(d / 100) % 100).padStart(2, '0')}-${String(d % 100).padStart(2, '0')}`
const short = (d: number) => `${String(Math.floor(d / 100) % 100).padStart(2, '0')}-${String(d % 100).padStart(2, '0')}`
const bar = (v: number) => (v > 90 ? 'error' : v > 75 ? 'warning' : 'success')

async function loadSummary() {
  try {
    summary.value = await api.get<TrafficUsage[]>('/traffic/summary')
  } catch (e) {
    message.error(errorText(e))
  }
}

async function loadDetail() {
  loading.value = true
  try {
    detail.value = await api.get<TrafficDetail>(`/nodes/${selected.value}/traffic`, { cycle: cycle.value })
  } catch (e) {
    detail.value = null
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadSummary()
  loadDetail()
})
watch(selected, (n) => {
  host.setState({ node: n })
  cycle.value = 0
  loadDetail()
})
watch(cycle, loadDetail)
watch(
  () => host.props.node as string | undefined,
  (v) => {
    if (v && v !== selected.value) selected.value = v
  },
)

const u = computed(() => detail.value?.usage)
const labels = computed(() => detail.value?.days.map((d) => short(d.day)) ?? [])
const daySeries = computed(() => [
  { name: t('monitor.rx'), data: detail.value?.days.map((d) => d.rx) ?? [] },
  { name: t('monitor.tx'), data: detail.value?.days.map((d) => d.tx) ?? [] },
])
const cycleOptions = computed(() =>
  (detail.value?.cycles ?? []).map((c, i) => ({ label: i === 0 ? `${t('traffic.current')}（${day(c.start)} ~ ${day(c.end)}）` : `${day(c.start)} ~ ${day(c.end)}`, value: i })),
)

/* ---------- 设置 ---------- */
const showSettings = ref(false)
const form = ref({ resetDay: 1, quotaGB: 0, quotaMode: 'sum' as 'sum' | 'rx' | 'tx' })
function openSettings() {
  const x = u.value
  form.value = { resetDay: x?.resetDay || 1, quotaGB: x?.quota ? +(x.quota / 1024 ** 3).toFixed(2) : 0, quotaMode: x?.quotaMode || 'sum' }
  showSettings.value = true
}
async function saveSettings() {
  try {
    await api.put(`/nodes/${selected.value}/traffic/settings`, {
      resetDay: form.value.resetDay,
      quota: Math.round((form.value.quotaGB || 0) * 1024 ** 3),
      quotaMode: form.value.quotaMode,
    })
    showSettings.value = false
    message.success(t('common.saved'))
    loadSummary()
    loadDetail()
  } catch (e) {
    message.error(errorText(e))
  }
}
</script>

<template>
  <div class="traffic sd-scroll" data-testid="traffic">
    <table class="sum">
      <thead>
        <tr>
          <th>{{ t('traffic.node') }}</th>
          <th>{{ t('traffic.cycle') }}</th>
          <th class="r">{{ t('monitor.rx') }}</th>
          <th class="r">{{ t('monitor.tx') }}</th>
          <th class="r">{{ t('traffic.total') }}</th>
          <th class="w">{{ t('traffic.quota') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr
          v-for="x in summary"
          :key="x.nodeId"
          :class="{ sel: x.nodeId === selected }"
          :data-testid="'traffic-row-' + x.nodeId"
          @click="selected = x.nodeId"
        >
          <td><i class="dot" :style="{ background: nodes.color(x.nodeId) }" />{{ nodes.name(x.nodeId) }}</td>
          <template v-if="x.supported">
            <td class="sd-muted">{{ short(x.start) }} ~ {{ short(x.end) }}</td>
            <td class="r">{{ formatBytes(x.rx) }}</td>
            <td class="r">{{ formatBytes(x.tx) }}</td>
            <td class="r"><b>{{ formatBytes(x.rx + x.tx) }}</b></td>
            <td>
              <NProgress v-if="x.quota" type="line" :percentage="Math.min(100, Math.round(x.percent))" :status="bar(x.percent)" :height="8">
                {{ x.percent.toFixed(0) }}%
              </NProgress>
              <span v-else class="sd-muted">{{ t('traffic.unlimited') }}</span>
            </td>
          </template>
          <td v-else colspan="5" class="sd-muted">{{ t('traffic.unsupported') }}</td>
        </tr>
      </tbody>
    </table>

    <div class="head">
      <b>{{ nodes.name(selected) }}</b>
      <NSelect v-model:value="cycle" :options="cycleOptions" size="small" style="width: 260px" />
      <span class="grow" />
      <NButton v-if="canManage" size="small" data-testid="traffic-settings" @click="openSettings">{{ t('traffic.settings') }}</NButton>
    </div>

    <NSpin :show="loading">
      <NAlert v-if="u && !u.supported" type="info" :bordered="false">{{ t('traffic.unsupportedLong') }}</NAlert>
      <template v-else-if="detail && u">
        <div class="cards">
          <div class="card">
            <div class="label">{{ t('monitor.rx') }}</div>
            <div class="value">{{ formatBytes(u.rx) }}</div>
          </div>
          <div class="card">
            <div class="label">{{ t('monitor.tx') }}</div>
            <div class="value">{{ formatBytes(u.tx) }}</div>
          </div>
          <div class="card">
            <div class="label">{{ t('traffic.total') }}</div>
            <div class="value">{{ formatBytes(u.rx + u.tx) }}</div>
          </div>
          <div class="card">
            <div class="label">{{ t('traffic.quota') }}（{{ t('traffic.mode.' + u.quotaMode) }}）</div>
            <div class="value">{{ u.quota ? `${u.percent.toFixed(1)}%` : t('traffic.unlimited') }}</div>
            <div v-if="u.quota" class="sd-muted small">{{ formatBytes(u.used) }} / {{ formatBytes(u.quota) }}</div>
            <div class="sd-muted small">{{ t('traffic.resetOn', { d: u.resetDay }) }}</div>
          </div>
        </div>
        <section>
          <h4>{{ t('traffic.daily') }}</h4>
          <div class="plot"><BarChart :labels="labels" :series="daySeries" :format="(v) => formatBytes(v)" /></div>
        </section>
        <div class="two">
          <section>
            <h4>{{ t('traffic.ifaces') }}</h4>
            <table class="sum">
              <tbody>
                <tr v-for="f in detail.ifaces" :key="f.iface">
                  <td class="sd-mono">{{ f.iface }}</td>
                  <td class="r">↓ {{ formatBytes(f.rx) }}</td>
                  <td class="r">↑ {{ formatBytes(f.tx) }}</td>
                  <td class="r"><b>{{ formatBytes(f.rx + f.tx) }}</b></td>
                </tr>
                <tr v-if="!detail.ifaces.length">
                  <td class="sd-muted">{{ t('traffic.noData') }}</td>
                </tr>
              </tbody>
            </table>
          </section>
          <section>
            <h4>{{ t('traffic.history') }}</h4>
            <table class="sum">
              <tbody>
                <tr v-for="(c, i) in detail.cycles" :key="c.start" class="click" @click="cycle = i">
                  <td class="sd-muted">{{ day(c.start) }} ~ {{ day(c.end) }}</td>
                  <td class="r">↓ {{ formatBytes(c.rx) }}</td>
                  <td class="r">↑ {{ formatBytes(c.tx) }}</td>
                  <td class="r"><b>{{ formatBytes(c.rx + c.tx) }}</b></td>
                </tr>
              </tbody>
            </table>
          </section>
        </div>
      </template>
    </NSpin>

    <NModal v-model:show="showSettings" preset="card" :title="t('traffic.settings')" style="width: 420px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="96" size="small">
        <NFormItem :label="t('traffic.resetDay')">
          <NInputNumber v-model:value="form.resetDay" :min="1" :max="28" style="width: 120px" data-testid="traffic-reset-day" />
        </NFormItem>
        <NFormItem :label="t('traffic.quotaGB')">
          <NInputNumber v-model:value="form.quotaGB" :min="0" :step="10" style="width: 160px" data-testid="traffic-quota" />
          <span class="sd-muted small" style="margin-left: 8px">{{ t('traffic.zeroUnlimited') }}</span>
        </NFormItem>
        <NFormItem :label="t('traffic.quotaMode')">
          <NRadioGroup v-model:value="form.quotaMode" size="small">
            <NRadioButton value="sum">{{ t('traffic.mode.sum') }}</NRadioButton>
            <NRadioButton value="rx">{{ t('traffic.mode.rx') }}</NRadioButton>
            <NRadioButton value="tx">{{ t('traffic.mode.tx') }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
      </NForm>
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px">
          <NButton @click="showSettings = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" data-testid="traffic-save" @click="saveSettings">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.traffic {
  height: 100%;
  padding: 12px 16px 20px;
  background: var(--sd-surface);
}
.sum {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.sum th {
  text-align: left;
  font-weight: normal;
  color: var(--sd-text-2);
  font-size: 12px;
  padding: 4px 8px;
}
.sum td {
  padding: 5px 8px;
  border-top: 1px solid var(--sd-border);
  white-space: nowrap;
}
.sum tbody tr {
  cursor: pointer;
}
.sum tbody tr:hover {
  background: var(--sd-hover);
}
.sum tr.sel {
  background: var(--sd-active);
}
.r {
  text-align: right;
}
.w {
  width: 28%;
}
.dot {
  display: inline-block;
  width: 8px;
  height: 8px;
  border-radius: 50%;
  margin-right: 6px;
}
.head {
  display: flex;
  gap: 10px;
  align-items: center;
  margin: 16px 0 10px;
}
.grow {
  flex: 1;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: 10px;
  margin-bottom: 10px;
}
.card,
section {
  padding: 10px 12px;
  border-radius: var(--sd-radius);
  background: var(--sd-surface-2);
  border: 1px solid var(--sd-border);
}
.label {
  font-size: 12px;
  color: var(--sd-text-2);
}
.value {
  font-size: 20px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.small {
  font-size: 12px;
}
h4 {
  margin: 0 0 6px;
  font-size: 13px;
  font-weight: 600;
}
.plot {
  height: 200px;
}
.two {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 10px;
  margin-top: 10px;
}
</style>
