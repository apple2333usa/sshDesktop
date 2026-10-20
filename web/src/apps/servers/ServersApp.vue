<script setup lang="ts">
// 服务器：多节点总览卡片墙（每台服务器一张卡片，5 秒刷新），以及添加、修改、删除、重新连接、
// 安装 agent、主机指纹变化后重新确认。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDropdown, NEmpty, NProgress, NTag, useDialog, useMessage, type DropdownOption } from 'naive-ui'
import { api } from '@/api/http'
import type { HostKeyScan, NodeView, Overview, Task, TrafficUsage } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useNodes } from '@/stores/nodes'
import { useNotify } from '@/stores/notify'
import { useSession } from '@/stores/session'
import { withDanger } from '@/components/danger'
import SdIcon from '@/components/SdIcon.vue'
import NodeDialog from './NodeDialog.vue'
import { errorText, formatBytes, formatDuration, formatRate } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()
const nodes = useNodes()
const notify = useNotify()
const session = useSession()

const canManage = computed(() => session.has('nodes:manage'))
const canMonitor = computed(() => session.has('monitor:view'))
const overview = ref<Record<string, Overview>>({})
const traffic = ref<Record<string, TrafficUsage>>({})
const showDialog = ref(false)
const editing = ref<NodeView | null>(null)

async function loadOverview() {
  if (!canMonitor.value) return
  try {
    const list = await api.get<Overview[]>('/monitor/overview')
    overview.value = Object.fromEntries(list.map((o) => [o.id, o]))
  } catch {
    /* 下次再试 */
  }
}

async function loadTraffic() {
  if (!canMonitor.value) return
  try {
    const list = await api.get<TrafficUsage[]>('/traffic/summary')
    traffic.value = Object.fromEntries(list.map((u) => [u.nodeId, u]))
  } catch {
    /* 忽略 */
  }
}

let timer: number | undefined
let tick = 0
function poll() {
  if (host.visible.value) {
    loadOverview()
    if (tick++ % 12 === 0) loadTraffic()
  }
}
onMounted(() => {
  nodes.load()
  poll()
  timer = window.setInterval(poll, 5000)
})
onUnmounted(() => clearInterval(timer))
watch(host.visible, (v) => v && loadOverview())

const pct = (a: number, b: number) => (b ? Math.min(100, (a / b) * 100) : 0)
const bar = (v: number) => (v > 90 ? 'error' : v > 75 ? 'warning' : 'success')

const stateText = (n: NodeView) => t('servers.state.' + n.status.state)
const stateType = (n: NodeView) =>
  ({ online: 'success', connecting: 'warning', offline: 'default', 'hostkey-mismatch': 'error', 'auth-failed': 'error' })[n.status.state] as
    | 'success'
    | 'warning'
    | 'default'
    | 'error'

function sourceText(o: Overview | undefined): string {
  if (!o || !o.source) return ''
  if (o.source === 'local') return t('servers.srcLocal')
  if (o.source === 'ssh') return t('servers.srcSSH')
  return `${o.source === 'panel' ? t('servers.srcPanel') : 'agent'} ${o.agentVersion ?? ''}`
}

/* ---------- 操作 ---------- */

function open(app: string, n: NodeView) {
  host.openApp(app, { node: n.id })
}

function add() {
  editing.value = null
  showDialog.value = true
}

function edit(n: NodeView) {
  editing.value = n
  showDialog.value = true
}

function onSaved(v: NodeView) {
  nodes.upsert(v)
  setTimeout(loadOverview, 1500)
}

async function reconnect(n: NodeView) {
  try {
    await api.post(`/nodes/${n.id}/reconnect`)
    message.info(t('servers.reconnecting'))
  } catch (e) {
    message.error(errorText(e))
  }
}

async function remove(n: NodeView) {
  try {
    const r = await withDanger(
      {
        level: 2,
        action: t('servers.deleteAction'),
        node: nodes.label(n.id),
        impact: [t('servers.deleteImpact')],
        confirmText: t('servers.deleteConfirm', { name: n.name }),
      },
      (pw) => api.del(`/nodes/${n.id}`, { confirmPassword: pw }),
    )
    if (r !== undefined) {
      nodes.list = nodes.list.filter((x) => x.id !== n.id)
      message.success(t('servers.deleted'))
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

async function runTask(path: string) {
  const task = await api.post<Task>(path)
  const done = await notify.waitTask(task)
  if (done.status === 'success') loadOverview()
  return done
}

async function installAgent(n: NodeView) {
  try {
    const r = await withDanger(
      {
        level: 1,
        action: t('servers.installAction'),
        node: nodes.label(n.id),
        impact: [t('servers.installImpact1'), t('servers.installImpact2')],
        confirmText: t('servers.install'),
      },
      async () => true,
    )
    if (r) await runTask(`/nodes/${n.id}/agent/install`)
  } catch (e) {
    message.error(errorText(e))
  }
}

async function uninstallAgent(n: NodeView) {
  try {
    const r = await withDanger(
      { level: 1, action: t('servers.uninstallAction'), node: nodes.label(n.id), impact: [t('servers.uninstallImpact')], confirmText: t('servers.uninstall') },
      async () => true,
    )
    if (r) await runTask(`/nodes/${n.id}/agent/uninstall`)
  } catch (e) {
    message.error(errorText(e))
  }
}

// 主机密钥变化：先取得新指纹，和旧指纹一起展示，确认后（二级）保存
async function acceptHostKey(n: NodeView) {
  let k: HostKeyScan
  try {
    k = await api.post<HostKeyScan>('/nodes/scan', { host: n.host, port: n.port })
  } catch (e) {
    message.error(errorText(e))
    return
  }
  try {
    const r = await withDanger(
      {
        level: 2,
        action: t('servers.acceptAction'),
        node: nodes.label(n.id),
        impact: [
          `${t('servers.oldFp')}：${n.hostKey ? `${n.hostKey.type} ${n.hostKey.fingerprint}` : '-'}`,
          `${t('servers.newFp')}：${k.keyType} ${k.fingerprint}`,
          t('servers.acceptWarn'),
        ],
        confirmText: t('servers.accept'),
      },
      (pw) => api.post<NodeView>(`/nodes/${n.id}/hostkey`, { key: k.key }, { confirmPassword: pw }),
    )
    if (r) {
      nodes.upsert(r)
      message.success(t('servers.accepted'))
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

function menu(n: NodeView): DropdownOption[] {
  const remote = n.kind === 'ssh'
  const o = overview.value[n.id]
  const items: DropdownOption[] = [
    { key: 'monitor', label: t('apps.monitor'), show: canMonitor.value },
    { key: 'explorer', label: t('apps.explorer'), show: session.has('files:view') },
    { key: 'terminal', label: t('apps.terminal'), show: session.has('terminal:use') },
    { key: 'traffic', label: t('apps.traffic'), show: canMonitor.value },
    { type: 'divider', key: 'd1', show: canManage.value },
    { key: 'edit', label: t('common.edit'), show: canManage.value },
    { key: 'reconnect', label: t('servers.reconnect'), show: canManage.value && remote },
    { key: 'hostkey', label: t('servers.accept'), show: canManage.value && remote && n.status.state === 'hostkey-mismatch' },
    { key: 'install', label: o?.source === 'agent' ? t('servers.reinstall') : t('servers.install'), show: canManage.value && remote && o?.source !== 'panel' },
    { key: 'uninstall', label: t('servers.uninstall'), show: canManage.value && remote && o?.source === 'agent' },
    { type: 'divider', key: 'd2', show: canManage.value && remote },
    { key: 'delete', label: t('common.delete'), show: canManage.value && remote, props: { style: 'color: var(--sd-danger, #d13438)' } },
  ]
  return items
}

function onMenu(key: string, n: NodeView) {
  switch (key) {
    case 'monitor':
    case 'explorer':
    case 'terminal':
    case 'traffic':
      return open(key, n)
    case 'edit':
      return edit(n)
    case 'reconnect':
      return reconnect(n)
    case 'hostkey':
      return acceptHostKey(n)
    case 'install':
      return installAgent(n)
    case 'uninstall':
      return uninstallAgent(n)
    case 'delete':
      return remove(n)
  }
}

function onCardOpen(n: NodeView) {
  if (n.status.state === 'hostkey-mismatch' && canManage.value) return acceptHostKey(n)
  if (canMonitor.value) open('monitor', n)
}

const summary = computed(() => t('servers.summary', { online: nodes.onlineCount, total: nodes.list.length }))

function noAgentTip() {
  dialog.info({ title: t('servers.noAgentTitle'), content: t('servers.noAgentTip'), positiveText: t('common.ok') })
}
</script>

<template>
  <div class="servers sd-scroll" data-testid="servers">
    <div class="toolbar">
      <b>{{ summary }}</b>
      <span class="grow" />
      <NButton size="small" quaternary @click="nodes.load(), loadOverview(), loadTraffic()">{{ t('explorer.refresh') }}</NButton>
      <NButton v-if="canManage" size="small" type="primary" data-testid="servers-add" @click="add">{{ t('servers.add') }}</NButton>
    </div>

    <NEmpty v-if="nodes.loaded && nodes.list.length <= 1 && canManage" class="empty" :description="t('servers.empty')" />

    <div class="grid">
      <div
        v-for="n in nodes.list"
        :key="n.id"
        class="card"
        :class="n.status.state"
        :style="{ '--node-color': nodes.color(n.id) }"
        :data-testid="'server-' + n.id"
        @dblclick="onCardOpen(n)"
      >
        <div class="top">
          <SdIcon :name="n.kind === 'local' ? 'thispc' : 'server'" :size="28" />
          <div class="title">
            <div class="name sd-ellipsis">{{ n.name }}</div>
            <div class="sd-muted small sd-ellipsis">
              {{ n.kind === 'local' ? t('servers.local') : `${n.username}@${n.host}:${n.port}` }}
            </div>
          </div>
          <NTag size="small" :bordered="false" :type="stateType(n)" data-testid="server-state">{{ stateText(n) }}</NTag>
          <NDropdown trigger="click" :options="menu(n)" @select="(k: string) => onMenu(k, n)">
            <NButton size="tiny" quaternary data-testid="server-menu">⋯</NButton>
          </NDropdown>
        </div>

        <div v-if="n.status.error && n.status.state !== 'online'" class="err small" data-testid="server-error">
          {{ n.status.error }}
          <NButton v-if="n.status.state === 'hostkey-mismatch' && canManage" size="tiny" type="error" secondary @click="acceptHostKey(n)">
            {{ t('servers.accept') }}
          </NButton>
        </div>

        <template v-if="overview[n.id]?.sample">
          <div class="os sd-muted small sd-ellipsis">
            {{ overview[n.id].info?.platform }} {{ overview[n.id].info?.platformVersion }} · {{ overview[n.id].info?.arch }} ·
            {{ t('monitor.uptime') }} {{ formatDuration(overview[n.id].sample!.uptime) }}
          </div>
          <div class="meters">
            <div class="m">
              <span>CPU</span>
              <NProgress type="line" :percentage="Math.round(overview[n.id].sample!.cpu)" :status="bar(overview[n.id].sample!.cpu)" :height="8" :show-indicator="false" />
              <span class="pv">{{ overview[n.id].sample!.cpu.toFixed(0) }}%</span>
            </div>
            <div class="m">
              <span>{{ t('monitor.memory') }}</span>
              <NProgress
                type="line"
                :percentage="Math.round(pct(overview[n.id].sample!.memUsed, overview[n.id].sample!.memTotal))"
                :status="bar(pct(overview[n.id].sample!.memUsed, overview[n.id].sample!.memTotal))"
                :height="8"
                :show-indicator="false"
              />
              <span class="pv">{{ pct(overview[n.id].sample!.memUsed, overview[n.id].sample!.memTotal).toFixed(0) }}%</span>
            </div>
            <div class="m">
              <span>{{ t('monitor.disk') }}</span>
              <NProgress
                type="line"
                :percentage="Math.round(pct(overview[n.id].sample!.diskUsed, overview[n.id].sample!.diskTotal))"
                :status="bar(pct(overview[n.id].sample!.diskUsed, overview[n.id].sample!.diskTotal))"
                :height="8"
                :show-indicator="false"
              />
              <span class="pv">{{ pct(overview[n.id].sample!.diskUsed, overview[n.id].sample!.diskTotal).toFixed(0) }}%</span>
            </div>
          </div>
          <div class="nums small">
            <span>↓ {{ formatRate(overview[n.id].sample!.netRxBps) }}</span>
            <span>↑ {{ formatRate(overview[n.id].sample!.netTxBps) }}</span>
            <span class="sd-muted">{{ t('monitor.load') }} {{ overview[n.id].sample!.load1.toFixed(2) }}</span>
          </div>
        </template>
        <div v-else-if="n.status.state === 'online'" class="sd-muted small wait">{{ t('servers.waiting') }}</div>

        <div class="foot small">
          <span v-if="traffic[n.id]?.supported" class="sd-muted" :title="t('servers.trafficTip')">
            {{ t('servers.trafficUsed', { used: formatBytes(traffic[n.id].used) }) }}
            <template v-if="traffic[n.id].quota"> / {{ formatBytes(traffic[n.id].quota) }}（{{ traffic[n.id].percent.toFixed(0) }}%）</template>
          </span>
          <span class="grow" />
          <span v-if="n.kind === 'ssh' && n.status.state === 'online' && n.status.latencyMs" class="sd-muted">{{ Math.round(n.status.latencyMs) }} ms</span>
          <NTag v-if="sourceText(overview[n.id])" size="tiny" :bordered="false" :type="overview[n.id]?.source === 'ssh' ? 'warning' : 'info'" data-testid="server-source">
            {{ sourceText(overview[n.id]) }}
          </NTag>
          <NButton v-if="overview[n.id]?.source === 'ssh'" text size="tiny" @click="noAgentTip">?</NButton>
        </div>
      </div>
    </div>

    <NodeDialog v-model:show="showDialog" :edit="editing" @saved="onSaved" />
  </div>
</template>

<style scoped>
.servers {
  height: 100%;
  padding: 12px 16px 20px;
  background: var(--sd-surface);
}
.toolbar {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 12px;
}
.grow {
  flex: 1;
}
.empty {
  margin: 12px 0 20px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 10px;
}
.card {
  position: relative;
  padding: 10px 12px 10px 15px;
  border-radius: var(--sd-radius);
  background: var(--sd-surface-2);
  border: 1px solid var(--sd-border);
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow: hidden;
  user-select: none;
}
.card::before {
  content: '';
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 4px;
  background: var(--node-color);
}
.card.offline,
.card.auth-failed {
  opacity: 0.75;
}
.card.hostkey-mismatch {
  border-color: #d13438;
}
.top {
  display: flex;
  align-items: center;
  gap: 8px;
}
.title {
  flex: 1;
  min-width: 0;
}
.name {
  font-weight: 600;
}
.small {
  font-size: 12px;
}
.err {
  color: #d13438;
  display: flex;
  gap: 6px;
  align-items: center;
  flex-wrap: wrap;
  word-break: break-all;
}
.meters {
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.m {
  display: grid;
  grid-template-columns: 40px 1fr 36px;
  align-items: center;
  font-size: 12px;
  gap: 6px;
}
.pv {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.nums {
  display: flex;
  gap: 12px;
  font-variant-numeric: tabular-nums;
}
.wait {
  padding: 8px 0;
}
.foot {
  display: flex;
  gap: 6px;
  align-items: center;
  min-height: 20px;
}
</style>
