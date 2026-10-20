<script setup lang="ts">
// 容器列表：状态、端口、资源占用，启动 / 停止 / 重启 / 删除，日志、终端、详情，新建容器。
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NButtonGroup, NCheckbox, NDataTable, NDropdown, NForm, NFormItem, NInput, NInputNumber, NModal, NRadioButton, NRadioGroup,
  NSelect, NSwitch, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api } from '@/api/http'
import type { DockerContainer, DockerStats, Task } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useNotify } from '@/stores/notify'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import LogStream from '@/components/LogStream.vue'
import TaskLog from '@/components/TaskLog.vue'
import { errorText, formatBytes, formatTime } from '@/i18n'
import { stateType, portText } from './util'

const props = defineProps<{ active: boolean }>()
const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const notify = useNotify()
const hostInfo = useHost()
const canManage = computed(() => session.has('docker:manage'))
const base = '/nodes/local/docker/containers'

const list = ref<DockerContainer[]>([])
const stats = ref<Record<string, DockerStats>>({})
const loading = ref(false)
const q = ref('')
const filter = ref<'all' | 'running' | 'stopped'>('all')
const selected = ref<string | null>(null)
const busy = ref<string | null>(null)
const logTask = ref<Task | null>(null)
let timer: number | undefined

async function load() {
  loading.value = true
  try {
    list.value = await api.get<DockerContainer[]>(base)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

async function loadStats() {
  if (!list.value.some((c) => c.state === 'running')) return
  try {
    stats.value = await api.get<Record<string, DockerStats>>(base + '/stats')
  } catch {
    /* 忽略 */
  }
}

function startTimer() {
  clearInterval(timer)
  timer = window.setInterval(() => {
    if (props.active) {
      load()
      loadStats()
    }
  }, 5000)
}
onMounted(() => load().then(loadStats).then(startTimer))
onUnmounted(() => clearInterval(timer))
watch(() => props.active, (v) => v && load().then(loadStats))

const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  return list.value.filter((c) => {
    if (filter.value === 'running' && c.state !== 'running') return false
    if (filter.value === 'stopped' && c.state === 'running') return false
    return !s || c.name.toLowerCase().includes(s) || c.image.toLowerCase().includes(s) || (c.project ?? '').includes(s)
  })
})
const runningCount = computed(() => list.value.filter((c) => c.state === 'running').length)
const cur = computed(() => list.value.find((c) => c.id === selected.value) ?? null)

/* ---------- 操作 ---------- */
async function action(c: DockerContainer, a: string) {
  busy.value = c.id + a
  try {
    const run = () => api.post(`${base}/${c.id}/${a}`)
    const r = ['stop', 'restart', 'kill'].includes(a)
      ? await withDanger(
          { level: 1, action: t('docker.containerAction', { action: t('docker.act.' + a) }), node: hostInfo.label, impact: c.name, confirmText: `${t('docker.act.' + a)} ${c.name}` },
          run,
        )
      : await run()
    if (r !== undefined) message.success(t('docker.done', { action: t('docker.act.' + a) }))
  } catch (e) {
    message.error(errorText(e))
  } finally {
    busy.value = null
  }
  load()
}

async function remove(c: DockerContainer) {
  try {
    const r = await withDanger(
      {
        level: 1,
        action: t('docker.deleteContainer'),
        node: hostInfo.label,
        impact: [c.name + '（' + c.image + '）', c.project ? t('docker.composeWarn', { project: c.project }) : t('docker.deleteContainerImpact')],
        confirmText: t('docker.deleteNamed', { name: c.name }),
      },
      () => api.del(`${base}/${c.id}`, { query: { force: '1' } }).then(() => true),
    )
    if (r) {
      if (selected.value === c.id) selected.value = null
      load()
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

async function prune() {
  try {
    const r = await withDanger(
      { level: 1, action: t('docker.pruneContainers'), node: hostInfo.label, impact: t('docker.pruneContainersImpact'), confirmText: t('docker.prune') },
      () => api.post<{ deleted: number; spaceReclaimed: number }>(base + '/prune'),
    )
    if (r) {
      message.success(t('docker.pruned', { n: r.deleted, size: formatBytes(r.spaceReclaimed) }))
      load()
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

const renaming = ref<DockerContainer | null>(null)
const newName = ref('')
async function rename() {
  if (!renaming.value) return
  try {
    await api.post(`${base}/${renaming.value.id}/rename`, { name: newName.value.trim() })
    renaming.value = null
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

function terminal(c: DockerContainer) {
  host.openApp('terminal', { node: 'local', container: c.id.slice(0, 12) })
}

function moreOptions(c: DockerContainer) {
  const running = c.state === 'running'
  return [
    { label: t('docker.logs'), key: 'logs' },
    { label: t('docker.terminal'), key: 'terminal', disabled: !running || !canManage.value },
    { type: 'divider', key: 'd1' },
    ...(canManage.value
      ? [
          c.state === 'paused' ? { label: t('docker.act.unpause'), key: 'unpause' } : { label: t('docker.act.pause'), key: 'pause', disabled: !running },
          { label: t('docker.act.kill'), key: 'kill', disabled: !running },
          { label: t('docker.rename'), key: 'rename' },
          { type: 'divider', key: 'd2' },
          { label: t('common.delete'), key: 'delete' },
        ]
      : []),
  ]
}

function onMore(c: DockerContainer, key: string) {
  if (key === 'logs') {
    selected.value = c.id
    sideTab.value = 'logs'
  } else if (key === 'terminal') terminal(c)
  else if (key === 'rename') {
    renaming.value = c
    newName.value = c.name
  } else if (key === 'delete') remove(c)
  else action(c, key)
}

const columns = computed<DataTableColumns<DockerContainer>>(() => [
  {
    title: t('docker.name'),
    key: 'name',
    minWidth: 180,
    ellipsis: { tooltip: true },
    sorter: (a, b) => a.name.localeCompare(b.name),
    render: (c) =>
      h('div', { class: 'cname' }, [
        h('span', { class: 'sd-mono' }, c.name),
        c.app ? h(NTag, { size: 'tiny', type: 'info', bordered: false }, () => t('docker.fromApp')) : c.project ? h(NTag, { size: 'tiny', bordered: false }, () => c.project) : null,
      ]),
  },
  { title: t('docker.image'), key: 'image', minWidth: 160, ellipsis: { tooltip: true }, render: (c) => h('span', { class: 'sd-mono' }, c.image) },
  {
    title: t('docker.state'),
    key: 'state',
    width: 150,
    sorter: (a, b) => a.state.localeCompare(b.state),
    render: (c) => h('div', { class: 'cstate' }, [h(NTag, { size: 'small', type: stateType(c.state), bordered: false }, () => t('docker.st.' + c.state, c.state)), h('span', { class: 'sd-muted small' }, c.status)]),
  },
  { title: t('docker.ports'), key: 'ports', minWidth: 120, ellipsis: { tooltip: true }, render: (c) => h('span', { class: 'sd-mono small' }, c.ports.filter((p) => p.public).map(portText).join(', ') || '-') },
  {
    title: 'CPU',
    key: 'cpu',
    width: 70,
    sorter: (a, b) => (stats.value[a.id]?.cpuPercent ?? -1) - (stats.value[b.id]?.cpuPercent ?? -1),
    render: (c) => (stats.value[c.id] ? stats.value[c.id].cpuPercent.toFixed(1) + '%' : '-'),
  },
  {
    title: t('docker.memory'),
    key: 'mem',
    width: 90,
    sorter: (a, b) => (stats.value[a.id]?.memUsage ?? -1) - (stats.value[b.id]?.memUsage ?? -1),
    render: (c) => (stats.value[c.id] ? formatBytes(stats.value[c.id].memUsage) : '-'),
  },
  {
    title: '',
    key: 'actions',
    width: 150,
    render: (c) =>
      h('div', { class: 'acts', onClick: (e: MouseEvent) => e.stopPropagation() }, [
        canManage.value
          ? h(NButtonGroup, { size: 'tiny' }, () =>
              (c.state === 'running' ? ['restart', 'stop'] : ['start']).map((a) =>
                h(NButton, { size: 'tiny', secondary: true, type: a === 'stop' ? 'error' : 'default', loading: busy.value === c.id + a, onClick: () => action(c, a), 'data-testid': `ct-${a}-${c.name}` }, () => t('docker.act.' + a)),
              ),
            )
          : null,
        h(NDropdown, { trigger: 'click', options: moreOptions(c), onSelect: (k: string) => onMore(c, k) }, () => h(NButton, { size: 'tiny', quaternary: true, 'data-testid': 'ct-more-' + c.name }, () => '⋯')),
      ]),
  },
])

const rowProps = (c: DockerContainer) => ({
  class: c.id === selected.value ? 'row-sel' : '',
  'data-testid': 'ct-' + c.name,
  onClick: () => (selected.value = c.id),
})

/* ---------- 详情 ---------- */
const sideTab = ref<'info' | 'logs'>('info')
const inspect = ref<any>(null)
watch(selected, async (id) => {
  inspect.value = null
  if (!id) return
  try {
    inspect.value = await api.get(`${base}/${id}`)
  } catch (e) {
    message.error(errorText(e))
  }
})
const env = computed<string[]>(() => inspect.value?.Config?.Env ?? [])

/* ---------- 新建 ---------- */
interface Form {
  name: string
  image: string
  command: string
  ports: { hostIp: string; host: string; container: string; proto: string }[]
  env: string
  volumes: { source: string; target: string; ro: boolean }[]
  restart: string
  network: string
  memoryMb: number | null
  cpus: number | null
  privileged: boolean
  tty: boolean
  pull: 'missing' | 'always'
}
const blank = (): Form => ({
  name: '', image: '', command: '', ports: [], env: '', volumes: [], restart: 'unless-stopped', network: '', memoryMb: null, cpus: null,
  privileged: false, tty: false, pull: 'missing',
})
const showCreate = ref(false)
const form = ref<Form>(blank())
const networks = ref<{ label: string; value: string }[]>([])
const creating = ref(false)

async function openCreate() {
  form.value = blank()
  showCreate.value = true
  try {
    const list = await api.get<{ name: string }[]>('/nodes/local/docker/networks')
    networks.value = list.map((n) => ({ label: n.name, value: n.name }))
  } catch {
    networks.value = []
  }
}

async function create() {
  creating.value = true
  try {
    const f = form.value
    const body = {
      ...f,
      memoryMb: f.memoryMb ?? 0,
      cpus: f.cpus ?? 0,
      env: f.env.split('\n').map((s) => s.trim()).filter(Boolean),
      ports: f.ports.filter((p) => p.container.trim()),
      volumes: f.volumes.filter((v) => v.source.trim() && v.target.trim()),
    }
    const task = await api.post<Task>(base, body)
    showCreate.value = false
    logTask.value = task
    const done = await notify.waitTask(task)
    if (done.status === 'success') load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    creating.value = false
  }
}
</script>

<template>
  <div class="ct">
    <div class="sd-toolbar">
      <NInput v-model:value="q" size="small" clearable :placeholder="t('docker.searchContainer')" style="max-width: 220px" />
      <NRadioGroup v-model:value="filter" size="small">
        <NRadioButton value="all">{{ t('services.all') }} {{ list.length }}</NRadioButton>
        <NRadioButton value="running">{{ t('docker.running') }} {{ runningCount }}</NRadioButton>
        <NRadioButton value="stopped">{{ t('docker.stopped') }} {{ list.length - runningCount }}</NRadioButton>
      </NRadioGroup>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <template v-if="canManage">
        <NButton size="small" @click="prune">{{ t('docker.pruneStopped') }}</NButton>
        <NButton size="small" type="primary" data-testid="ct-create" @click="openCreate">{{ t('docker.createContainer') }}</NButton>
      </template>
    </div>
    <div class="main">
      <NDataTable
        class="sd-fill"
        size="small"
        flex-height
        :loading="loading && !list.length"
        :columns="columns"
        :data="shown"
        :row-key="(c: DockerContainer) => c.id"
        :row-props="rowProps"
        :bordered="false"
      />
      <div v-if="cur" class="side">
        <div class="side-head">
          <b class="sd-mono sd-ellipsis">{{ cur.name }}</b>
          <NButton size="tiny" quaternary @click="selected = null">×</NButton>
        </div>
        <NTabs v-model:value="sideTab" size="small" type="line" class="stabs">
          <NTabPane name="info" :tab="t('services.info')">
            <div class="sd-scroll infobox">
              <dl class="kv">
                <dt>ID</dt>
                <dd class="sd-mono">{{ cur.id.slice(0, 12) }}</dd>
                <dt>{{ t('docker.image') }}</dt>
                <dd class="sd-mono">{{ cur.image }}</dd>
                <dt>{{ t('docker.command') }}</dt>
                <dd class="sd-mono">{{ cur.command }}</dd>
                <dt>{{ t('docker.created') }}</dt>
                <dd>{{ formatTime(cur.created * 1000) }}</dd>
                <dt>{{ t('docker.state') }}</dt>
                <dd>{{ cur.status }}</dd>
                <template v-if="cur.project">
                  <dt>Compose</dt>
                  <dd>{{ cur.project }} / {{ cur.service }}</dd>
                </template>
                <dt>{{ t('docker.ports') }}</dt>
                <dd class="sd-mono">
                  <div v-for="p in cur.ports" :key="p.private + p.proto + (p.public ?? '')">{{ portText(p) }}</div>
                  <span v-if="!cur.ports.length">-</span>
                </dd>
                <dt>{{ t('docker.networks') }}</dt>
                <dd>{{ cur.networks.map((n, i) => n + (cur!.ips[i] ? ' ' + cur!.ips[i] : '')).join('，') || '-' }}</dd>
                <dt>{{ t('docker.mounts') }}</dt>
                <dd class="sd-mono">
                  <div v-for="m in cur.mounts" :key="m.destination">{{ m.name || m.source }} → {{ m.destination }}{{ m.rw ? '' : ' (ro)' }}</div>
                  <span v-if="!cur.mounts.length">-</span>
                </dd>
                <template v-if="inspect">
                  <dt>{{ t('docker.restartPolicy') }}</dt>
                  <dd>{{ inspect.HostConfig?.RestartPolicy?.Name || 'no' }}</dd>
                  <dt>{{ t('docker.env') }}</dt>
                  <dd class="sd-mono">
                    <div v-for="e in env" :key="e" class="env">{{ e }}</div>
                  </dd>
                </template>
              </dl>
              <div class="ops">
                <NButton v-if="canManage && cur.state === 'running'" size="small" data-testid="ct-terminal" @click="terminal(cur)">{{ t('docker.terminal') }}</NButton>
              </div>
            </div>
          </NTabPane>
          <NTabPane name="logs" :tab="t('docker.logs')">
            <LogStream :path="`${base}/${cur.id}/logs?tail=500`" :active="sideTab === 'logs' && props.active" />
          </NTabPane>
        </NTabs>
      </div>
    </div>

    <NModal :show="!!renaming" preset="card" :title="t('docker.rename')" style="width: 400px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && (renaming = null)">
      <NInput v-model:value="newName" @keyup.enter="rename" />
      <template #footer>
        <div class="foot">
          <NButton @click="renaming = null">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :disabled="!newName.trim()" @click="rename">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>

    <NModal v-model:show="showCreate" preset="card" :title="t('docker.createContainer')" style="width: 640px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="96" size="small" class="cform sd-scroll">
        <NFormItem :label="t('docker.image')" required>
          <NInput v-model:value="form.image" placeholder="nginx:alpine" data-testid="ct-image" />
        </NFormItem>
        <NFormItem :label="t('docker.name')">
          <NInput v-model:value="form.name" :placeholder="t('docker.namePh')" data-testid="ct-name" />
        </NFormItem>
        <NFormItem :label="t('docker.command')">
          <NInput v-model:value="form.command" :placeholder="t('docker.commandPh')" />
        </NFormItem>
        <NFormItem :label="t('docker.ports')">
          <div class="rows">
            <div v-for="(p, i) in form.ports" :key="i" class="row">
              <NSelect v-model:value="p.hostIp" size="small" style="width: 130px" :options="[{ label: t('docker.allIPs'), value: '' }, { label: '127.0.0.1', value: '127.0.0.1' }]" />
              <NInput v-model:value="p.host" size="small" :placeholder="t('docker.hostPort')" style="width: 100px" />
              <span>:</span>
              <NInput v-model:value="p.container" size="small" :placeholder="t('docker.containerPort')" style="width: 100px" />
              <NSelect v-model:value="p.proto" size="small" style="width: 80px" :options="[{ label: 'tcp', value: 'tcp' }, { label: 'udp', value: 'udp' }]" />
              <NButton size="tiny" quaternary @click="form.ports.splice(i, 1)">×</NButton>
            </div>
            <NButton size="tiny" dashed @click="form.ports.push({ hostIp: '', host: '', container: '', proto: 'tcp' })">{{ t('docker.addPort') }}</NButton>
          </div>
        </NFormItem>
        <NFormItem :label="t('docker.mounts')">
          <div class="rows">
            <div v-for="(v, i) in form.volumes" :key="i" class="row">
              <NInput v-model:value="v.source" size="small" :placeholder="t('docker.mountSource')" />
              <span>→</span>
              <NInput v-model:value="v.target" size="small" :placeholder="t('docker.mountTarget')" />
              <NCheckbox v-model:checked="v.ro">{{ t('docker.readonly') }}</NCheckbox>
              <NButton size="tiny" quaternary @click="form.volumes.splice(i, 1)">×</NButton>
            </div>
            <NButton size="tiny" dashed @click="form.volumes.push({ source: '', target: '', ro: false })">{{ t('docker.addMount') }}</NButton>
          </div>
        </NFormItem>
        <NFormItem :label="t('docker.env')">
          <NInput v-model:value="form.env" type="textarea" :autosize="{ minRows: 2, maxRows: 6 }" placeholder="KEY=VALUE" class="sd-mono" />
        </NFormItem>
        <NFormItem :label="t('docker.restartPolicy')">
          <NSelect
            v-model:value="form.restart"
            :options="['no', 'always', 'unless-stopped', 'on-failure'].map((v) => ({ label: t('docker.restart.' + v), value: v }))"
            style="width: 220px"
          />
        </NFormItem>
        <NFormItem :label="t('docker.network')">
          <NSelect v-model:value="form.network" clearable :options="networks" :placeholder="t('docker.networkDefault')" style="width: 220px" />
        </NFormItem>
        <NFormItem :label="t('docker.limits')">
          <div class="row">
            <NInputNumber v-model:value="form.memoryMb" :min="0" size="small" :placeholder="t('docker.unlimited')" style="width: 140px">
              <template #suffix>MB</template>
            </NInputNumber>
            <NInputNumber v-model:value="form.cpus" :min="0" :step="0.5" size="small" :placeholder="t('docker.unlimited')" style="width: 120px">
              <template #suffix>CPU</template>
            </NInputNumber>
          </div>
        </NFormItem>
        <NFormItem :label="t('docker.options')">
          <div class="row">
            <NCheckbox v-model:checked="form.tty">{{ t('docker.tty') }}</NCheckbox>
            <NCheckbox v-model:checked="form.privileged">{{ t('docker.privileged') }}</NCheckbox>
            <span class="opt">{{ t('docker.pullAlways') }} <NSwitch size="small" :value="form.pull === 'always'" @update:value="(v: boolean) => (form.pull = v ? 'always' : 'missing')" /></span>
          </div>
        </NFormItem>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="showCreate = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="creating" :disabled="!form.image.trim()" data-testid="ct-create-ok" @click="create">{{ t('docker.createAndStart') }}</NButton>
        </div>
      </template>
    </NModal>
    <TaskLog v-model:task="logTask" />
  </div>
</template>

<style scoped>
.ct {
  height: 100%;
  display: flex;
  flex-direction: column;
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
:deep(.cname),
:deep(.cstate),
:deep(.acts) {
  display: flex;
  gap: 6px;
  align-items: center;
  min-width: 0;
}
:deep(.small) {
  font-size: 12px;
}
.side {
  width: 42%;
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
}
.stabs {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.stabs :deep(.n-tabs-pane-wrapper) {
  flex: 1;
  min-height: 0;
}
.stabs :deep(.n-tab-pane) {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.infobox {
  flex: 1;
  min-height: 0;
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
.env {
  font-size: 12px;
}
.ops {
  display: flex;
  gap: 6px;
  margin-top: 12px;
}
.cform {
  max-height: 64vh;
  padding-right: 6px;
}
.rows {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
}
.row {
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
}
.opt {
  display: inline-flex;
  gap: 6px;
  align-items: center;
}
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
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
