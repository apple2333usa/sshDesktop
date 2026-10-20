<script setup lang="ts">
// 进程：平铺 / 树形列表，按 CPU、内存排序，搜索，详情（命令行、打开的文件、网络连接），发送信号。
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NDataTable, NDropdown, NInput, NSwitch, NTabPane, NTabs, NTag, useMessage, type DataTableColumns, type DropdownOption,
} from 'naive-ui'
import { api } from '@/api/http'
import type { Proc } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import { errorText, formatBytes, formatTime } from '@/i18n'

type Row = Proc & { children?: Row[] }

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const hostInfo = useHost()
const base = '/nodes/local/processes'


const procs = ref<Proc[]>([])
const memTotal = ref(0)
const self = ref(0)
const q = ref('')
const tree = ref(!!host.props.tree)
const paused = ref(false)
const loading = ref(false)
const selected = ref<number | null>(null)
const detail = ref<Record<string, any> | null>(null)
const canManage = computed(() => session.has('process:manage'))

watch(tree, (v) => host.setState({ tree: v }))

async function load() {
  try {
    const r = await api.get<{ processes: Proc[]; memTotal: number; self: number }>(base)
    procs.value = r.processes
    memTotal.value = r.memTotal
    self.value = r.self
  } catch (e) {
    message.error(errorText(e))
    paused.value = true
  } finally {
    loading.value = false
  }
}

let timer: number | undefined
function schedule() {
  clearTimeout(timer)
  timer = window.setTimeout(async () => {
    if (!paused.value && host.visible.value) await load()
    schedule()
  }, 2000)
}
onMounted(() => {
  loading.value = true
  load()
  schedule()
})
onUnmounted(() => clearTimeout(timer))

const filtered = computed(() => {
  const s = q.value.trim().toLowerCase()
  if (!s) return procs.value
  return procs.value.filter((p) => p.name.toLowerCase().includes(s) || p.cmdline.toLowerCase().includes(s) || String(p.pid) === s || p.user === s)
})

const rows = computed<Row[]>(() => {
  if (!tree.value || q.value.trim()) return filtered.value
  const map = new Map<number, Row>()
  procs.value.forEach((p) => map.set(p.pid, { ...p }))
  const roots: Row[] = []
  map.forEach((r) => {
    const parent = map.get(r.ppid)
    if (parent && parent !== r) (parent.children ??= []).push(r)
    else roots.push(r)
  })
  return roots
})

const total = computed(() => {
  const cpu = procs.value.reduce((a, p) => a + p.cpu, 0)
  const rss = procs.value.reduce((a, p) => a + p.rss, 0)
  return { n: procs.value.length, cpu, rss }
})

/* ---------- 信号 ---------- */
const signalNames: Record<string, string> = {
  TERM: 'processes.sigTerm',
  KILL: 'processes.sigKill',
  HUP: 'processes.sigHup',
  INT: 'processes.sigInt',
  STOP: 'processes.sigStop',
  CONT: 'processes.sigCont',
  USR1: 'SIGUSR1',
  USR2: 'SIGUSR2',
}
const signalOptions = computed<DropdownOption[]>(() =>
  Object.entries(signalNames).map(([k, v]) => ({ key: k, label: v.startsWith('SIG') ? v : `${t(v)} (SIG${k})` })),
)

async function signal(p: Proc, sig: string) {
  const label = signalNames[sig].startsWith('SIG') ? signalNames[sig] : t(signalNames[sig])
  try {
    const r = await withDanger(
      {
        level: 1,
        action: t('processes.signalAction', { sig: label }),
        node: hostInfo.label,
        impact: `${p.pid}  ${p.name}  (${p.user})\n${p.cmdline}`,
        confirmText: t('processes.signalConfirm', { sig: label, name: p.name }),
      },
      () => api.post(`${base}/${p.pid}/signal`, { signal: sig }),
    )
    if (r !== undefined) message.success(t('processes.signalSent'))
  } catch (e) {
    message.error(errorText(e))
  }
  load()
}

async function showDetail(pid: number) {
  selected.value = pid
  detail.value = null
  try {
    detail.value = await api.get<Record<string, any>>(`${base}/${pid}`)
  } catch (e) {
    message.error(errorText(e))
    selected.value = null
  }
}

const selectedProc = computed(() => procs.value.find((p) => p.pid === selected.value))

/* ---------- 表格 ---------- */
const columns = computed<DataTableColumns<Row>>(() => [
  {
    title: t('processes.name'),
    key: 'name',
    width: 220,
    ellipsis: { tooltip: true },
    sorter: (a, b) => a.name.localeCompare(b.name),
    render: (r) => (r.pid === self.value ? h('span', [r.name, ' ', h(NTag, { size: 'tiny', type: 'info' }, () => t('processes.panel'))]) : r.name),
  },
  { title: 'PID', key: 'pid', width: 80, sorter: (a, b) => a.pid - b.pid },
  { title: t('processes.user'), key: 'user', width: 90, ellipsis: { tooltip: true }, sorter: (a, b) => a.user.localeCompare(b.user) },
  {
    title: 'CPU',
    key: 'cpu',
    width: 80,
    sorter: (a, b) => a.cpu - b.cpu,
    defaultSortOrder: tree.value ? false : 'descend',
    render: (r) => r.cpu.toFixed(1) + '%',
  },
  {
    title: t('processes.memory'),
    key: 'rss',
    width: 100,
    sorter: (a, b) => a.rss - b.rss,
    render: (r) => formatBytes(r.rss),
  },
  { title: t('processes.threads'), key: 'threads', width: 70, sorter: (a, b) => a.threads - b.threads },
  { title: t('processes.state'), key: 'state', width: 60 },
  { title: t('processes.started'), key: 'started', width: 140, sorter: (a, b) => a.started - b.started, render: (r) => formatTime(r.started) },
  { title: t('processes.command'), key: 'cmdline', ellipsis: { tooltip: true }, minWidth: 200, render: (r) => h('span', { class: 'sd-mono sd-muted' }, r.cmdline) },
  {
    title: '',
    key: 'actions',
    width: 120,
    fixed: 'right',
    render: (r) =>
      h('div', { style: 'display:flex;gap:4px' }, [
        h(NButton, { size: 'tiny', quaternary: true, onClick: (e: MouseEvent) => (e.stopPropagation(), showDetail(r.pid)) }, () => t('processes.details')),
        canManage.value && r.pid > 1 && r.pid !== self.value
          ? h(
              NDropdown,
              { options: signalOptions.value, trigger: 'click', onSelect: (k: string) => signal(r, k) },
              () => h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: (e: MouseEvent) => e.stopPropagation() }, () => t('processes.end') + ' ▾'),
            )
          : null,
      ]),
  },
])

const rowProps = (r: Row) => ({
  style: 'cursor: default',
  'data-testid': 'proc-' + r.pid,
  onDblclick: () => showDetail(r.pid),
})

const fmtConn = (c: any) => `${c.laddr?.ip ?? ''}:${c.laddr?.port ?? ''} → ${c.raddr?.ip ? `${c.raddr.ip}:${c.raddr.port}` : '*'} ${c.status ?? ''}`
</script>

<template>
  <div class="sd-page procs" data-testid="processes">
    <div class="sd-toolbar">
      <NInput v-model:value="q" size="small" clearable :placeholder="t('processes.search')" style="max-width: 260px" data-testid="proc-search" />
      <span class="opt">{{ t('processes.tree') }} <NSwitch v-model:value="tree" size="small" /></span>
      <span class="opt">{{ t('processes.pause') }} <NSwitch v-model:value="paused" size="small" /></span>
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <span class="sd-muted sum">{{ t('processes.summary', { n: total.n, cpu: total.cpu.toFixed(1), mem: formatBytes(total.rss) }) }}</span>
    </div>
    <div class="main">
      <NDataTable
        class="sd-fill"
        size="small"
        flex-height
        virtual-scroll
        :loading="loading"
        :columns="columns"
        :data="rows"
        :row-key="(r: Row) => r.pid"
        :row-props="rowProps"
        :bordered="false"
        :default-expand-all="true"
        :scroll-x="1180"
      />
      <div v-if="selected !== null" class="side sd-scroll">
        <div class="side-head">
          <b>{{ selectedProc?.name ?? detail?.name }} ({{ selected }})</b>
          <NButton size="tiny" quaternary @click="selected = null">×</NButton>
        </div>
        <NTabs v-if="detail" size="small" type="line">
          <NTabPane name="info" :tab="t('processes.general')">
            <dl class="kv">
              <dt>{{ t('processes.command') }}</dt>
              <dd class="sd-mono">{{ (detail.cmdline || []).join(' ') }}</dd>
              <dt>{{ t('processes.exe') }}</dt>
              <dd class="sd-mono">{{ detail.exe || '-' }}</dd>
              <dt>{{ t('processes.cwd') }}</dt>
              <dd class="sd-mono">{{ detail.cwd || '-' }}</dd>
              <dt>{{ t('processes.user') }}</dt>
              <dd>{{ detail.username }}</dd>
              <dt>{{ t('processes.parent') }}</dt>
              <dd>{{ detail.ppid }}</dd>
              <dt>{{ t('processes.state') }}</dt>
              <dd>{{ (detail.status || []).join(', ') }}</dd>
              <dt>Nice</dt>
              <dd>{{ detail.nice }}</dd>
              <dt>{{ t('processes.threads') }}</dt>
              <dd>{{ detail.threads }}</dd>
              <dt>{{ t('processes.fds') }}</dt>
              <dd>{{ detail.fds }}</dd>
              <dt>RSS / VMS</dt>
              <dd>{{ formatBytes(detail.memory?.rss ?? 0) }} / {{ formatBytes(detail.memory?.vms ?? 0) }}</dd>
              <dt>{{ t('processes.io') }}</dt>
              <dd>R {{ formatBytes(detail.io?.readBytes ?? 0) }} · W {{ formatBytes(detail.io?.writeBytes ?? 0) }}</dd>
              <dt>{{ t('processes.started') }}</dt>
              <dd>{{ formatTime(detail.createTime, true) }}</dd>
              <dt v-if="detail.children?.length">{{ t('processes.children') }}</dt>
              <dd v-if="detail.children?.length">{{ detail.children.join(', ') }}</dd>
            </dl>
          </NTabPane>
          <NTabPane name="files" :tab="t('processes.openFiles') + ` (${detail.openFiles?.length ?? 0})`">
            <div v-if="!detail.openFiles?.length" class="sd-muted">{{ t('processes.none') }}</div>
            <div v-for="f in detail.openFiles" :key="f.fd" class="line sd-mono">{{ f.fd }}　{{ f.path }}</div>
          </NTabPane>
          <NTabPane name="conns" :tab="t('processes.connections') + ` (${detail.connections?.length ?? 0})`">
            <div v-if="!detail.connections?.length" class="sd-muted">{{ t('processes.none') }}</div>
            <div v-for="(c, i) in detail.connections" :key="i" class="line sd-mono">{{ c.type === 1 ? 'TCP' : c.type === 2 ? 'UDP' : c.type }}　{{ fmtConn(c) }}</div>
          </NTabPane>
        </NTabs>
      </div>
    </div>
  </div>
</template>

<style scoped>
.procs {
  background: var(--sd-surface);
}
.opt {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
}
.sum {
  margin-left: auto;
  font-size: 12px;
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
.side {
  width: 360px;
  flex-shrink: 0;
  border-left: 1px solid var(--sd-border);
  padding: 8px 12px;
  font-size: 13px;
}
.side-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.kv {
  display: grid;
  grid-template-columns: 80px 1fr;
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
.line {
  font-size: 12px;
  padding: 2px 0;
  word-break: break-all;
}
@media (max-width: 760px) {
  .side {
    position: absolute;
    inset: 0;
    width: auto;
    background: var(--sd-surface);
    z-index: 2;
  }
}
</style>
