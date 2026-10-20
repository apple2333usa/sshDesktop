<script setup lang="ts">
// 系统服务：systemd 单元列表、状态、启动 / 停止 / 重启 / 开机自启，详情和 journalctl 实时日志。
import { computed, h, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NButtonGroup, NDataTable, NInput, NRadioButton, NRadioGroup, NSwitch, NTabPane, NTabs, NTag, useMessage,
  type DataTableColumns,
} from 'naive-ui'
import { api, wsURL } from '@/api/http'
import type { Unit } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import { errorText, formatBytes, formatDuration } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const hostInfo = useHost()
const base = '/nodes/local/services'

host.setTitle(t('apps.services'))

const units = ref<Unit[]>([])
const loading = ref(false)
const q = ref('')
const filter = ref<'all' | 'running' | 'failed' | 'enabled'>('all')
const selected = ref<string | null>((host.props.unit as string) || null)
const busy = ref<string | null>(null)
const canManage = computed(() => session.has('process:manage'))

async function load() {
  loading.value = true
  try {
    units.value = await api.get<Unit[]>(base)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  return units.value.filter((u) => {
    if (filter.value === 'running' && u.sub !== 'running') return false
    if (filter.value === 'failed' && u.active !== 'failed') return false
    if (filter.value === 'enabled' && u.unitFile !== 'enabled') return false
    return !s || u.name.toLowerCase().includes(s) || u.description.toLowerCase().includes(s)
  })
})

const counts = computed(() => ({
  running: units.value.filter((u) => u.sub === 'running').length,
  failed: units.value.filter((u) => u.active === 'failed').length,
}))

function activeTag(u: Unit) {
  const type = u.active === 'active' ? 'success' : u.active === 'failed' ? 'error' : u.active === 'activating' || u.active === 'reloading' ? 'warning' : 'default'
  return h(NTag, { size: 'small', type, bordered: false }, () => `${u.active} (${u.sub})`)
}

/* ---------- 操作 ---------- */
const dangerous = new Set(['stop', 'restart', 'disable'])

async function control(name: string, action: string) {
  const label = t('services.' + action)
  busy.value = name + action
  try {
    const run = () => api.post<{ output: string }>(`${base}/${encodeURIComponent(name)}/${action}`)
    const r = dangerous.has(action)
      ? await withDanger(
          { level: 1, action: t('services.controlAction', { action: label }), node: hostInfo.label, impact: name, confirmText: `${label} ${name}` },
          run,
        )
      : await run()
    if (r !== undefined) message.success(r.output || t('services.done', { action: label }))
  } catch (e) {
    message.error(errorText(e))
  } finally {
    busy.value = null
  }
  await load()
  if (selected.value === name) loadDetail()
}

const columns = computed<DataTableColumns<Unit>>(() => [
  {
    title: t('services.name'),
    key: 'name',
    width: 260,
    ellipsis: { tooltip: true },
    sorter: (a, b) => a.name.localeCompare(b.name),
    render: (u) => h('span', { class: 'sd-mono' }, u.name.replace(/\.service$/, '')),
  },
  { title: t('services.status'), key: 'active', width: 150, sorter: (a, b) => a.active.localeCompare(b.active), render: activeTag },
  {
    title: t('services.autostart'),
    key: 'unitFile',
    width: 100,
    render: (u) =>
      u.unitFile === 'enabled' || u.unitFile === 'disabled'
        ? h(NSwitch, {
            size: 'small',
            value: u.unitFile === 'enabled',
            disabled: !canManage.value,
            loading: busy.value === u.name + (u.unitFile === 'enabled' ? 'disable' : 'enable'),
            onClick: (e: MouseEvent) => e.stopPropagation(),
            'onUpdate:value': (v: boolean) => control(u.name, v ? 'enable' : 'disable'),
          })
        : h('span', { class: 'sd-muted' }, u.unitFile || '-'),
  },
  { title: t('services.description'), key: 'description', ellipsis: { tooltip: true } },
  {
    title: '',
    key: 'actions',
    width: 180,
    render: (u) =>
      canManage.value
        ? h(NButtonGroup, { size: 'tiny' }, () =>
            (u.active === 'active' ? ['restart', 'stop'] : ['start']).map((a) =>
              h(
                NButton,
                {
                  size: 'tiny',
                  loading: busy.value === u.name + a,
                  type: a === 'stop' ? 'error' : 'default',
                  secondary: true,
                  onClick: (e: MouseEvent) => (e.stopPropagation(), control(u.name, a)),
                },
                () => t('services.' + a),
              ),
            ),
          )
        : null,
  },
])

const rowProps = (u: Unit) => ({
  style: 'cursor: default',
  class: u.name === selected.value ? 'row-sel' : '',
  'data-testid': 'svc-' + u.name,
  onClick: () => {
    selected.value = u.name
  },
})

/* ---------- 详情与日志 ---------- */
const detail = ref<{ props: Record<string, string>; unitFile: string } | null>(null)
const tab = ref<'info' | 'logs' | 'file'>('info')
const logs = ref<string[]>([])
const follow = ref(true)
const logBox = ref<HTMLElement | null>(null)
let ws: WebSocket | null = null

async function loadDetail() {
  if (!selected.value) return
  try {
    detail.value = await api.get(`${base}/${encodeURIComponent(selected.value)}`)
  } catch (e) {
    message.error(errorText(e))
  }
}

function closeLogs() {
  ws?.close()
  ws = null
}

function openLogs() {
  closeLogs()
  logs.value = []
  if (!selected.value) return
  const sock = new WebSocket(wsURL(`${base}/${encodeURIComponent(selected.value)}/logs?lines=300`))
  ws = sock
  sock.onmessage = (ev) => {
    try {
      const lines = JSON.parse(ev.data) as string[]
      const next = logs.value.concat(lines)
      logs.value = next.length > 5000 ? next.slice(next.length - 5000) : next
      if (follow.value) nextTick(() => logBox.value && (logBox.value.scrollTop = logBox.value.scrollHeight))
    } catch {
      /* 忽略 */
    }
  }
}

watch(
  selected,
  (v) => {
    host.setState({ unit: v })
    detail.value = null
    if (!v) return closeLogs()
    loadDetail()
    if (tab.value === 'logs') openLogs()
  },
  { immediate: true },
)
watch(tab, (v) => (v === 'logs' ? openLogs() : closeLogs()))
watch(host.visible, (v) => {
  if (!v) closeLogs()
  else if (tab.value === 'logs' && selected.value) openLogs()
})
onUnmounted(closeLogs)

const p = computed(() => detail.value?.props ?? {})
function since(ts: string) {
  if (!ts) return '-'
  const d = new Date(ts.replace(/^\w+ /, '').replace(/ \w+$/, ''))
  if (Number.isNaN(d.getTime())) return ts
  return `${ts}（${formatDuration((Date.now() - d.getTime()) / 1000)}）`
}
const num = (v?: string) => (v && /^\d+$/.test(v) && v !== '18446744073709551615' ? Number(v) : null)
</script>

<template>
  <div class="sd-page svc" data-testid="services">
    <div class="sd-toolbar">
      <NInput v-model:value="q" size="small" clearable :placeholder="t('services.search')" style="max-width: 240px" data-testid="svc-search" />
      <NRadioGroup v-model:value="filter" size="small">
        <NRadioButton value="all">{{ t('services.all') }} {{ units.length }}</NRadioButton>
        <NRadioButton value="running">{{ t('services.running') }} {{ counts.running }}</NRadioButton>
        <NRadioButton value="failed">{{ t('services.failed') }} {{ counts.failed }}</NRadioButton>
        <NRadioButton value="enabled">{{ t('services.enabled') }}</NRadioButton>
      </NRadioGroup>
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
    </div>
    <div class="main">
      <NDataTable
        class="sd-fill"
        size="small"
        flex-height
        virtual-scroll
        :loading="loading"
        :columns="columns"
        :data="shown"
        :row-key="(u: Unit) => u.name"
        :row-props="rowProps"
        :bordered="false"
      />
      <div v-if="selected" class="side">
        <div class="side-head">
          <b class="sd-mono sd-ellipsis">{{ selected }}</b>
          <NButton size="tiny" quaternary @click="selected = null">×</NButton>
        </div>
        <NTabs v-model:value="tab" size="small" type="line" class="tabs">
          <NTabPane name="info" :tab="t('services.info')">
            <dl v-if="detail" class="kv">
              <dt>{{ t('services.description') }}</dt>
              <dd>{{ p.Description }}</dd>
              <dt>{{ t('services.status') }}</dt>
              <dd>{{ p.ActiveState }} ({{ p.SubState }})</dd>
              <dt>{{ t('services.autostart') }}</dt>
              <dd>{{ p.UnitFileState || '-' }}</dd>
              <dt>{{ t('services.since') }}</dt>
              <dd>{{ since(p.ActiveEnterTimestamp) }}</dd>
              <dt>{{ t('services.mainPid') }}</dt>
              <dd>{{ p.MainPID && p.MainPID !== '0' ? p.MainPID : '-' }}</dd>
              <dt>{{ t('services.memory') }}</dt>
              <dd>{{ num(p.MemoryCurrent) !== null ? formatBytes(num(p.MemoryCurrent)!) : '-' }}</dd>
              <dt>{{ t('services.cpuTime') }}</dt>
              <dd>{{ num(p.CPUUsageNSec) !== null ? (num(p.CPUUsageNSec)! / 1e9).toFixed(1) + ' s' : '-' }}</dd>
              <dt>{{ t('services.tasks') }}</dt>
              <dd>{{ num(p.TasksCurrent) ?? '-' }}</dd>
              <dt>{{ t('services.type') }}</dt>
              <dd>{{ p.Type }} · Restart={{ p.Restart }}{{ p.User ? ' · User=' + p.User : '' }}</dd>
              <dt>{{ t('services.path') }}</dt>
              <dd class="sd-mono">{{ p.FragmentPath || '-' }}</dd>
            </dl>
            <div v-if="canManage && detail" class="ops">
              <NButton v-for="a in ['start', 'stop', 'restart', 'reload']" :key="a" size="small" :loading="busy === selected + a" @click="control(selected!, a)">
                {{ t('services.' + a) }}
              </NButton>
            </div>
          </NTabPane>
          <NTabPane name="logs" :tab="t('services.logs')">
            <div class="logbar">
              <span class="opt">{{ t('services.follow') }} <NSwitch v-model:value="follow" size="small" /></span>
              <NButton size="tiny" quaternary @click="openLogs">{{ t('explorer.refresh') }}</NButton>
            </div>
            <pre ref="logBox" class="logs sd-mono" data-testid="svc-logs">{{ logs.join('\n') }}</pre>
          </NTabPane>
          <NTabPane name="file" :tab="t('services.unitFile')">
            <pre class="logs sd-mono">{{ detail?.unitFile }}</pre>
          </NTabPane>
        </NTabs>
      </div>
    </div>
  </div>
</template>

<style scoped>
.svc {
  background: var(--sd-surface);
}
.main {
  flex: 1;
  min-height: 0;
  display: flex;
  position: relative;
}
.main > .sd-fill {
  flex: 1;
  min-width: 0;
}
:deep(.row-sel td) {
  background: var(--sd-select) !important;
}
.side {
  width: 44%;
  min-width: 320px;
  flex-shrink: 0;
  border-left: 1px solid var(--sd-border);
  padding: 8px 12px;
  display: flex;
  flex-direction: column;
  font-size: 13px;
}
.side-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}
.tabs {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.tabs :deep(.n-tabs-pane-wrapper) {
  flex: 1;
  min-height: 0;
}
.tabs :deep(.n-tab-pane) {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.kv {
  display: grid;
  grid-template-columns: 90px 1fr;
  gap: 6px 8px;
  margin: 0;
}
.kv dt {
  color: var(--sd-text-2);
}
.kv dd {
  margin: 0;
  word-break: break-all;
}
.ops {
  display: flex;
  gap: 6px;
  margin-top: 14px;
}
.logbar {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 6px;
}
.opt {
  display: inline-flex;
  gap: 6px;
  align-items: center;
}
.logs {
  flex: 1;
  min-height: 0;
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
@media (max-width: 760px) {
  .side {
    position: absolute;
    inset: 0;
    width: auto;
    min-width: 0;
    background: var(--sd-surface);
    z-index: 2;
  }
}
</style>
