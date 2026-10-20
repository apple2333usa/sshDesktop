<script setup lang="ts">
// 服务监控：HTTP(S)、TCP 端口、Ping、SSL 证书到期。面板按间隔探测，结果保存 30 天，
// 列表显示最近 24 小时状态条和可用率，选中后看延迟曲线。异常与恢复进入通知中心。
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NEmpty, NForm, NFormItem, NInput, NInputNumber, NModal, NRadioButton, NRadioGroup, NSpin, NSwitch, NTag, useMessage,
} from 'naive-ui'
import { api } from '@/api/http'
import { realtime } from '@/api/realtime'
import type { Probe, ProbeResult, ProbeType, ProbeView } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { withDanger } from '@/components/danger'
import LineChart, { type ChartSeries } from '@/components/LineChart.vue'
import { errorText, formatTime } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const canManage = computed(() => session.has('probe:manage'))

const list = ref<ProbeView[]>([])
const loading = ref(false)
const selected = ref<number | null>((host.props.probe as number) ?? null)
const range = ref<'24h' | '7d' | '30d'>('24h')
const results = shallowRef<ProbeResult[]>([])
let unsub: (() => void) | null = null
let timer: number | undefined

async function load() {
  loading.value = true
  try {
    list.value = await api.get<ProbeView[]>('/probes')
    if (selected.value === null && list.value.length) selected.value = list.value[0].id
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

async function loadResults() {
  if (selected.value === null) return
  const now = Math.floor(Date.now() / 1000)
  const from = now - { '24h': 86400, '7d': 7 * 86400, '30d': 30 * 86400 }[range.value]
  try {
    results.value = await api.get<ProbeResult[]>(`/probes/${selected.value}/results`, { from, to: now })
  } catch (e) {
    message.error(errorText(e))
  }
}

onMounted(() => {
  load().then(loadResults)
  unsub = realtime.subscribe('probes', (ev: { probeId: number; ts: number; result: Omit<ProbeResult, 'ts'> }) => {
    const p = list.value.find((x) => x.id === ev.probeId)
    if (p) p.last = { ...ev.result, ts: ev.ts }
    if (ev.probeId === selected.value && range.value === '24h') results.value = [...results.value, { ...ev.result, ts: ev.ts }]
  })
  // 可用率和状态条每分钟重新统计
  timer = window.setInterval(() => host.visible.value && load(), 60000)
})
onUnmounted(() => {
  unsub?.()
  clearInterval(timer)
})
watch(selected, (id) => {
  host.setState({ probe: id })
  loadResults()
})
watch(range, loadResults)

const cur = computed(() => list.value.find((p) => p.id === selected.value) ?? null)
const fmtMs = (v: number) => (Number.isNaN(v) ? '-' : `${v < 10 ? v.toFixed(1) : Math.round(v)} ms`)
const fmtUp = (v: number) => (v < 0 ? '-' : `${v >= 99.995 ? '100' : v.toFixed(2)}%`)
const latency = computed<ChartSeries[]>(() => [
  { name: t('probes.latency'), data: results.value.map((r) => [r.ts * 1000, r.ok ? r.latencyMs : NaN] as [number, number]) },
])
const failures = computed(() => results.value.filter((r) => !r.ok).slice(-20).reverse())
const bucketColor = (v: number) => (v < 0 ? 'var(--sd-active)' : v >= 1 ? '#16a34a' : v <= 0 ? '#dc2626' : '#d97706')
const typeLabel = (x: ProbeType) => ({ http: 'HTTP', tcp: 'TCP', ping: 'Ping', ssl: 'SSL' })[x]

/* ---------- 新建与修改 ---------- */
type Form = Omit<Probe, 'id' | 'createdAt' | 'updatedAt'>
const blank = (): Form => ({ name: '', type: 'http', target: '', interval: 60, timeout: 10, keyword: '', expectStatus: '200-399', sslWarnDays: 14, enabled: true })
const showForm = ref(false)
const editing = ref<number | null>(null)
const form = ref<Form>(blank())
const testResult = ref<{ ok: boolean; latencyMs: number; message: string } | null>(null)
const busy = ref(false)

const placeholders: Record<ProbeType, string> = {
  http: 'https://example.com/health',
  tcp: 'example.com:443',
  ping: 'example.com',
  ssl: 'example.com',
}

function openForm(p?: ProbeView) {
  editing.value = p?.id ?? null
  form.value = p
    ? { name: p.name, type: p.type, target: p.target, interval: p.interval, timeout: p.timeout, keyword: p.keyword, expectStatus: p.expectStatus, sslWarnDays: p.sslWarnDays, enabled: p.enabled }
    : blank()
  testResult.value = null
  showForm.value = true
}

async function test() {
  busy.value = true
  testResult.value = null
  try {
    testResult.value = await api.post('/probes/test', form.value)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    busy.value = false
  }
}

async function save() {
  busy.value = true
  try {
    const v = editing.value ? await api.put<ProbeView>(`/probes/${editing.value}`, form.value) : await api.post<ProbeView>('/probes', form.value)
    showForm.value = false
    message.success(t('common.saved'))
    await load()
    selected.value = v.id
    loadResults()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    busy.value = false
  }
}

async function remove(p: ProbeView) {
  try {
    const r = await withDanger(
      { level: 1, action: t('probes.deleteAction'), node: t('probes.panel'), impact: [`${p.name}（${p.target}）`, t('probes.deleteImpact')], confirmText: t('common.delete') },
      () => api.del(`/probes/${p.id}`).then(() => true),
    )
    if (r) {
      if (selected.value === p.id) selected.value = null
      load()
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

async function toggle(p: ProbeView, enabled: boolean) {
  try {
    await api.put(`/probes/${p.id}`, { ...p, enabled })
    p.enabled = enabled
  } catch (e) {
    message.error(errorText(e))
  }
}
</script>

<template>
  <div class="probes" data-testid="probes">
    <div class="toolbar">
      <b>{{ t('probes.summary', { n: list.length, down: list.filter((p) => p.enabled && p.last && !p.last.ok).length }) }}</b>
      <span class="grow" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <NButton v-if="canManage" size="small" type="primary" data-testid="probe-add" @click="openForm()">{{ t('probes.add') }}</NButton>
    </div>
    <div class="body">
      <NSpin :show="loading" class="listwrap">
        <div class="list sd-scroll">
          <div
            v-for="p in list"
            :key="p.id"
            class="item"
            :class="{ sel: p.id === selected, off: !p.enabled }"
            :data-testid="'probe-' + p.id"
            @click="selected = p.id"
          >
            <div class="row1">
              <i class="dot" :class="!p.enabled || !p.last ? 'none' : p.last.ok ? 'up' : 'down'" />
              <b class="sd-ellipsis">{{ p.name }}</b>
              <NTag size="tiny" :bordered="false">{{ typeLabel(p.type) }}</NTag>
              <span class="grow" />
              <span class="sd-muted small">{{ fmtUp(p.stats.uptime24h) }}</span>
            </div>
            <div class="sd-muted small sd-ellipsis">{{ p.target }}</div>
            <div class="buckets" :title="t('probes.last24h')">
              <i v-for="(v, i) in p.recent" :key="i" :style="{ background: bucketColor(v) }" />
            </div>
          </div>
          <NEmpty v-if="!list.length && !loading" class="empty" :description="t('probes.empty')" />
        </div>
      </NSpin>

      <div v-if="cur" class="detail sd-scroll">
        <div class="dhead">
          <div class="grow">
            <div class="title">{{ cur.name }}</div>
            <div class="sd-muted small sd-mono">{{ typeLabel(cur.type) }} · {{ cur.target }} · {{ t('probes.every', { n: cur.interval }) }}</div>
          </div>
          <template v-if="canManage">
            <NSwitch :value="cur.enabled" size="small" @update:value="(v: boolean) => toggle(cur!, v)" />
            <NButton size="small" @click="openForm(cur)">{{ t('common.edit') }}</NButton>
            <NButton size="small" type="error" secondary @click="remove(cur)">{{ t('common.delete') }}</NButton>
          </template>
        </div>
        <NAlert v-if="cur.last && !cur.last.ok" type="error" :bordered="false" class="mb">
          {{ formatTime(cur.last.ts * 1000, true) }}：{{ cur.last.message }}
        </NAlert>
        <NAlert v-else-if="cur.last" type="success" :bordered="false" class="mb">
          {{ formatTime(cur.last.ts * 1000, true) }}：{{ cur.last.message || t('probes.ok') }} · {{ fmtMs(cur.last.latencyMs) }}
        </NAlert>
        <div class="cards">
          <div class="card">
            <div class="label">{{ t('probes.uptime24h') }}</div>
            <div class="value">{{ fmtUp(cur.stats.uptime24h) }}</div>
          </div>
          <div class="card">
            <div class="label">{{ t('probes.uptime7d') }}</div>
            <div class="value">{{ fmtUp(cur.stats.uptime7d) }}</div>
          </div>
          <div class="card">
            <div class="label">{{ t('probes.uptime30d') }}</div>
            <div class="value">{{ fmtUp(cur.stats.uptime30d) }}</div>
          </div>
          <div class="card">
            <div class="label">{{ t('probes.avgLatency') }}</div>
            <div class="value">{{ cur.stats.avgLatency24h > 0 ? fmtMs(cur.stats.avgLatency24h) : '-' }}</div>
          </div>
        </div>
        <section>
          <div class="shead">
            <h4>{{ t('probes.latency') }}</h4>
            <NRadioGroup v-model:value="range" size="small">
              <NRadioButton value="24h">{{ t('monitor.d1') }}</NRadioButton>
              <NRadioButton value="7d">{{ t('monitor.d7') }}</NRadioButton>
              <NRadioButton value="30d">{{ t('monitor.d30') }}</NRadioButton>
            </NRadioGroup>
          </div>
          <div class="plot"><LineChart :series="latency" :format="fmtMs" :long="range !== '24h'" area /></div>
        </section>
        <section class="mt">
          <h4>{{ t('probes.failures') }}</h4>
          <div v-for="r in failures" :key="r.ts" class="fail small">
            <span class="sd-muted nowrap">{{ formatTime(r.ts * 1000, true) }}</span>
            <span>{{ r.message }}</span>
          </div>
          <div v-if="!failures.length" class="sd-muted small">{{ t('probes.noFailures') }}</div>
        </section>
      </div>
    </div>

    <NModal v-model:show="showForm" preset="card" :title="editing ? t('probes.edit') : t('probes.add')" style="width: 520px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="96" size="small">
        <NFormItem :label="t('probes.type')">
          <NRadioGroup v-model:value="form.type" size="small">
            <NRadioButton value="http">HTTP(S)</NRadioButton>
            <NRadioButton value="tcp">TCP</NRadioButton>
            <NRadioButton value="ping">Ping</NRadioButton>
            <NRadioButton value="ssl">{{ t('probes.sslCert') }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem :label="t('probes.target')">
          <NInput v-model:value="form.target" :placeholder="placeholders[form.type]" data-testid="probe-target" />
        </NFormItem>
        <NFormItem :label="t('probes.name')">
          <NInput v-model:value="form.name" :placeholder="t('probes.namePh')" maxlength="64" data-testid="probe-name" />
        </NFormItem>
        <template v-if="form.type === 'http'">
          <NFormItem :label="t('probes.expectStatus')">
            <NInput v-model:value="form.expectStatus" placeholder="200-399" />
          </NFormItem>
          <NFormItem :label="t('probes.keyword')">
            <NInput v-model:value="form.keyword" :placeholder="t('probes.keywordPh')" />
          </NFormItem>
        </template>
        <NFormItem v-if="form.type === 'ssl'" :label="t('probes.sslWarn')">
          <NInputNumber v-model:value="form.sslWarnDays" :min="1" :max="365" style="width: 120px">
            <template #suffix>{{ t('settings.days') }}</template>
          </NInputNumber>
        </NFormItem>
        <NFormItem :label="t('probes.interval')">
          <NInputNumber v-model:value="form.interval" :min="30" :max="86400" :step="30" style="width: 140px">
            <template #suffix>s</template>
          </NInputNumber>
        </NFormItem>
        <NFormItem :label="t('probes.timeout')">
          <NInputNumber v-model:value="form.timeout" :min="1" :max="60" style="width: 140px">
            <template #suffix>s</template>
          </NInputNumber>
        </NFormItem>
        <NFormItem :label="t('probes.enabled')">
          <NSwitch v-model:value="form.enabled" />
        </NFormItem>
      </NForm>
      <NAlert v-if="testResult" :type="testResult.ok ? 'success' : 'error'" :bordered="false">
        {{ testResult.ok ? t('probes.ok') : t('probes.down') }} · {{ fmtMs(testResult.latencyMs) }} {{ testResult.message }}
      </NAlert>
      <template #footer>
        <div style="display: flex; gap: 8px">
          <NButton :loading="busy" data-testid="probe-test" @click="test">{{ t('probes.test') }}</NButton>
          <span style="flex: 1" />
          <NButton @click="showForm = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :disabled="busy || !form.target.trim()" data-testid="probe-save" @click="save">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.probes {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--sd-surface);
}
.toolbar {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 10px 12px;
  border-bottom: 1px solid var(--sd-border);
}
.grow {
  flex: 1;
  min-width: 0;
}
.body {
  flex: 1;
  min-height: 0;
  display: flex;
}
.listwrap {
  width: 300px;
  flex: none;
  border-right: 1px solid var(--sd-border);
}
.listwrap :deep(.n-spin-content) {
  height: 100%;
}
.list {
  height: 100%;
  padding: 6px;
}
.item {
  padding: 8px;
  border-radius: 4px;
  cursor: pointer;
  display: flex;
  flex-direction: column;
  gap: 3px;
}
.item:hover {
  background: var(--sd-hover);
}
.item.sel {
  background: var(--sd-active);
}
.item.off {
  opacity: 0.6;
}
.row1 {
  display: flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  flex: none;
}
.dot.up {
  background: #16a34a;
}
.dot.down {
  background: #dc2626;
}
.dot.none {
  background: #8a8a8a;
}
.buckets {
  display: flex;
  gap: 1px;
  height: 14px;
}
.buckets i {
  flex: 1;
  border-radius: 1px;
}
.small {
  font-size: 12px;
}
.empty {
  margin-top: 40px;
}
.detail {
  flex: 1;
  min-width: 0;
  padding: 12px 16px 20px;
}
.dhead {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 10px;
}
.title {
  font-size: 16px;
  font-weight: 600;
}
.mb {
  margin-bottom: 10px;
}
.mt {
  margin-top: 10px;
}
.cards {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(140px, 1fr));
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
.shead {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 6px;
}
h4 {
  margin: 0 0 6px;
  font-size: 13px;
  font-weight: 600;
}
.plot {
  height: 200px;
}
.fail {
  display: flex;
  gap: 10px;
  padding: 3px 0;
  border-top: 1px solid var(--sd-border);
}
.nowrap {
  white-space: nowrap;
}
@media (max-width: 640px) {
  .body {
    flex-direction: column;
  }
  .listwrap {
    width: auto;
    max-height: 45%;
    border-right: 0;
    border-bottom: 1px solid var(--sd-border);
  }
}
</style>
