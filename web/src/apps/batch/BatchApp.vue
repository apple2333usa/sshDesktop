<script setup lang="ts">
// 批量命令：选择多个节点，输入命令或选用命令片段，并发执行（默认并发 5，可设超时），
// 按节点分栏显示输出和退出码。执行前一级确认，确认框列出所有目标节点。
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCheckbox, NDropdown, NEmpty, NInput, NInputNumber, NModal, NTabPane, NTabs, NTag, useMessage,
} from 'naive-ui'
import { api } from '@/api/http'
import { realtime } from '@/api/realtime'
import type { BatchEvent, BatchResult, BatchRun, BatchStatus, Snippet } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useNodes } from '@/stores/nodes'
import { withDanger } from '@/components/danger'
import { errorText, formatTime } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const nodes = useNodes()

const tab = ref<'run' | 'history'>('run')
const picked = ref<string[]>((host.props.nodes as string[]) ?? [])
const command = ref((host.props.command as string) ?? '')
const timeout = ref(60)
const concurrency = ref(5)
const run = ref<BatchRun | null>(null)
const results = ref<Record<string, BatchResult>>({})
const history = ref<BatchRun[]>([])
const snippets = ref<Snippet[]>([])
let unsub: (() => void) | null = null

const allIds = computed(() => nodes.list.map((n) => n.id))
const allPicked = computed(() => picked.value.length > 0 && picked.value.length === allIds.value.length)
function toggleAll(v: boolean) {
  picked.value = v ? [...allIds.value] : []
}

async function loadSnippets() {
  try {
    snippets.value = await api.get<Snippet[]>('/batch/snippets')
  } catch {
    /* 忽略 */
  }
}
async function loadHistory() {
  try {
    history.value = await api.get<BatchRun[]>('/batch/runs')
  } catch (e) {
    message.error(errorText(e))
  }
}
onMounted(async () => {
  await nodes.load()
  if (!picked.value.length) picked.value = nodes.list.filter((n) => n.status.state === 'online').map((n) => n.id)
  loadSnippets()
  loadHistory()
  if (host.props.runId) show(host.props.runId as string)
})
onUnmounted(() => unsub?.())

/* ---------- 执行 ---------- */

// 每个节点已拼接的输出片段数，和推送的 seq 对齐（见后端 batch.Event）
const seqs: Record<string, number> = {}

function apply(r: BatchRun) {
  run.value = r
  results.value = Object.fromEntries((r.results ?? []).map((x) => [x.nodeId, { ...x }]))
  for (const x of r.results ?? []) seqs[x.nodeId] = x.seq ?? 0
  for (const x of r.results ?? []) scrollDown(x.nodeId)
}

function follow(r: BatchRun) {
  unsub?.()
  unsub = null
  apply(r)
  host.setState({ runId: r.id })
  if (r.running) {
    unsub = realtime.subscribe(`batch:${r.id}`, onEvent)
    // 订阅生效前推送的输出和状态从查询结果里补上
    setTimeout(() => resync(r.id), 300)
  }
}

let resyncing = false
async function resync(id: string) {
  if (resyncing) return
  resyncing = true
  try {
    const r = await api.get<BatchRun>(`/batch/runs/${id}`)
    if (run.value?.id !== r.id) return
    apply(r)
    if (!r.running) {
      unsub?.()
      unsub = null
      loadHistory()
    }
  } catch {
    /* 忽略 */
  } finally {
    resyncing = false
  }
}

function onEvent(e: BatchEvent) {
  if (!run.value || e.runId !== run.value.id) return
  if (e.finished) {
    run.value.running = false
    unsub?.()
    unsub = null
    // 以服务端保存的最终结果为准
    resyncing = false
    resync(e.runId)
    return
  }
  const r = results.value[e.nodeId]
  if (!r) return
  if (e.status) r.status = e.status
  if (e.output) {
    const last = seqs[e.nodeId] ?? 0
    const seq = e.seq ?? last + 1
    if (seq === last + 1) {
      r.output += e.output
      seqs[e.nodeId] = seq
      scrollDown(e.nodeId)
    } else if (seq > last + 1) resync(e.runId) // 有片段缺失
  }
  if (e.exitCode !== undefined) r.exitCode = e.exitCode
  if (e.error) r.error = e.error
  if (e.durationMs) r.durationMs = e.durationMs
}

const outs: Record<string, HTMLElement | null> = {}
function scrollDown(id: string) {
  nextTick(() => {
    const el = outs[id]
    if (el && el.scrollHeight - el.scrollTop - el.clientHeight < 60) el.scrollTop = el.scrollHeight
  })
}

async function start() {
  const cmd = command.value.trim()
  if (!cmd) return message.warning(t('batch.needCommand'))
  if (!picked.value.length) return message.warning(t('batch.needNodes'))
  // 保持节点列表顺序
  const ids = allIds.value.filter((id) => picked.value.includes(id))
  const offline = ids.filter((id) => !nodes.online(id))
  try {
    const r = await withDanger(
      {
        level: 1,
        action: t('batch.action', { n: ids.length }),
        node: ids.map((id) => nodes.name(id)).join('、'),
        impact: [...ids.map((id) => nodes.label(id) + (nodes.online(id) ? '' : `（${t('batch.offline')}）`)), '$ ' + cmd],
        confirmText: t('batch.runOn', { n: ids.length }),
      },
      () => api.post<BatchRun>('/batch/runs', { nodes: ids, command: cmd, timeout: timeout.value, concurrency: concurrency.value }),
    )
    if (!r) return
    if (offline.length) message.warning(t('batch.offlineWarn', { n: offline.length }))
    follow(r)
    host.setState({ nodes: ids, command: cmd })
  } catch (e) {
    message.error(errorText(e))
  }
}

async function cancel() {
  if (!run.value) return
  try {
    await api.post(`/batch/runs/${run.value.id}/cancel`)
  } catch (e) {
    message.error(errorText(e))
  }
}

async function show(id: string) {
  try {
    const r = await api.get<BatchRun>(`/batch/runs/${id}`)
    follow(r)
    tab.value = 'run'
  } catch (e) {
    message.error(errorText(e))
  }
}

function rerun(r: BatchRun) {
  command.value = r.command
  picked.value = r.nodes.split(',').filter((id) => nodes.map[id])
  timeout.value = r.timeout
  tab.value = 'run'
}

const statusType = (s: BatchStatus) =>
  ({ pending: 'default', running: 'info', done: 'success', failed: 'error', timeout: 'warning', canceled: 'warning' })[s] as 'default'

const resultList = computed(() => (run.value?.results ?? []).map((x) => results.value[x.nodeId]).filter(Boolean))
const counts = computed(() => {
  const c = { done: 0, failed: 0, other: 0 }
  for (const r of resultList.value) {
    if (r.status === 'done' && r.exitCode === 0) c.done++
    else if (r.status === 'pending' || r.status === 'running') c.other++
    else c.failed++
  }
  return c
})

/* ---------- 命令片段 ---------- */

const snippetOptions = computed(() =>
  snippets.value.length
    ? snippets.value.map((s) => ({ key: s.id, label: s.name }))
    : [{ key: 0, label: t('batch.noSnippets'), disabled: true }],
)
function useSnippet(id: number) {
  const s = snippets.value.find((x) => x.id === id)
  if (s) command.value = s.command
}

const showSave = ref(false)
const snippetName = ref('')
async function saveSnippet() {
  const name = snippetName.value.trim()
  if (!name || !command.value.trim()) return
  try {
    await api.post('/batch/snippets', { name, command: command.value })
    showSave.value = false
    snippetName.value = ''
    message.success(t('common.saved'))
    loadSnippets()
  } catch (e) {
    message.error(errorText(e))
  }
}
async function deleteSnippet(s: Snippet) {
  try {
    await api.del(`/batch/snippets/${s.id}`)
    loadSnippets()
  } catch (e) {
    message.error(errorText(e))
  }
}

const showManage = ref(false)
</script>

<template>
  <div class="batch" data-testid="batch">
    <NTabs v-model:value="tab" type="line" size="small" class="tabs" @update:value="(v: string) => v === 'history' && loadHistory()">
      <NTabPane name="run" :tab="t('batch.run')">
        <div class="run">
          <aside class="side sd-scroll">
            <div class="sec">
              <NCheckbox :checked="allPicked" :indeterminate="picked.length > 0 && !allPicked" @update:checked="toggleAll">
                {{ t('batch.nodes') }}（{{ picked.length }}/{{ allIds.length }}）
              </NCheckbox>
            </div>
            <div class="nodes">
              <label v-for="n in nodes.list" :key="n.id" class="node" :data-testid="'batch-node-' + n.id">
                <input v-model="picked" type="checkbox" :value="n.id" />
                <i class="dot" :class="n.kind === 'local' ? 'online' : n.status.state" />
                <span class="sd-ellipsis">{{ n.name }}</span>
              </label>
            </div>
            <div class="opts">
              <label>{{ t('batch.timeout') }}</label>
              <NInputNumber v-model:value="timeout" :min="1" :max="3600" size="small" style="width: 100px">
                <template #suffix>s</template>
              </NInputNumber>
              <label>{{ t('batch.concurrency') }}</label>
              <NInputNumber v-model:value="concurrency" :min="1" :max="20" size="small" style="width: 100px" />
            </div>
          </aside>
          <main class="main">
            <div class="cmd">
              <NInput
                v-model:value="command"
                type="textarea"
                class="sd-mono"
                :autosize="{ minRows: 3, maxRows: 8 }"
                :placeholder="t('batch.placeholder')"
                data-testid="batch-command"
                @keydown.ctrl.enter.prevent="start"
              />
              <div class="cmdbar">
                <NDropdown trigger="click" :options="snippetOptions" @select="useSnippet">
                  <NButton size="small">{{ t('batch.snippets') }}</NButton>
                </NDropdown>
                <NButton size="small" quaternary :disabled="!command.trim()" @click="showSave = true">{{ t('batch.saveSnippet') }}</NButton>
                <NButton size="small" quaternary @click="showManage = true">{{ t('batch.manageSnippets') }}</NButton>
                <span class="grow" />
                <span class="sd-muted small">Ctrl+Enter</span>
                <NButton v-if="run?.running" size="small" @click="cancel">{{ t('batch.cancel') }}</NButton>
                <NButton size="small" type="primary" :disabled="!!run?.running" data-testid="batch-run" @click="start">{{ t('batch.execute') }}</NButton>
              </div>
            </div>
            <div v-if="run" class="summary small">
              <span class="sd-mono sd-ellipsis">$ {{ run.command }}</span>
              <span class="grow" />
              <NTag size="small" :bordered="false" type="success">{{ t('batch.ok', { n: counts.done }) }}</NTag>
              <NTag v-if="counts.failed" size="small" :bordered="false" type="error">{{ t('batch.fail', { n: counts.failed }) }}</NTag>
              <NTag v-if="counts.other" size="small" :bordered="false" type="info">{{ t('batch.pending', { n: counts.other }) }}</NTag>
            </div>
            <div v-if="run" class="panes">
              <div v-for="r in resultList" :key="r.nodeId" class="pane" :data-testid="'batch-result-' + r.nodeId">
                <div class="phead" :style="{ borderTopColor: nodes.color(r.nodeId) }">
                  <b class="sd-ellipsis">{{ nodes.name(r.nodeId) }}</b>
                  <span class="grow" />
                  <span v-if="r.durationMs" class="sd-muted small">{{ (r.durationMs / 1000).toFixed(1) }}s</span>
                  <NTag size="tiny" :bordered="false" :type="r.status === 'done' && r.exitCode !== 0 ? 'error' : statusType(r.status)" data-testid="batch-status">
                    {{ t('batch.status.' + r.status) }}<template v-if="r.status === 'done'"> · {{ r.exitCode }}</template>
                  </NTag>
                </div>
                <pre :ref="(el) => (outs[r.nodeId] = el as HTMLElement)" class="out sd-mono sd-scroll" data-testid="batch-output">{{ r.output }}<span v-if="r.error" class="perr">{{ r.error }}</span></pre>
              </div>
            </div>
            <NEmpty v-else class="empty" :description="t('batch.hint')" />
          </main>
        </div>
      </NTabPane>
      <NTabPane name="history" :tab="t('batch.history')">
        <div class="hist sd-scroll">
          <table>
            <thead>
              <tr>
                <th>{{ t('audit.time') }}</th>
                <th>{{ t('audit.user') }}</th>
                <th>{{ t('batch.command') }}</th>
                <th>{{ t('batch.nodes') }}</th>
                <th>{{ t('audit.result') }}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              <tr v-for="r in history" :key="r.id" @dblclick="show(r.id)">
                <td class="nowrap">{{ formatTime(r.createdAt, true) }}</td>
                <td>{{ r.username }}</td>
                <td class="sd-mono cmdcell sd-ellipsis" :title="r.command">{{ r.command }}</td>
                <td class="sd-ellipsis">{{ r.nodes.split(',').map((id) => nodes.name(id)).join('、') }}</td>
                <td class="nowrap">
                  <NTag v-if="r.running" size="tiny" type="info" :bordered="false">{{ t('batch.status.running') }}</NTag>
                  <template v-else>
                    <NTag size="tiny" type="success" :bordered="false">
                      {{ (r.results ?? []).filter((x) => x.status === 'done' && x.exitCode === 0).length }}
                    </NTag>
                    /
                    <NTag size="tiny" type="error" :bordered="false">
                      {{ (r.results ?? []).filter((x) => !(x.status === 'done' && x.exitCode === 0)).length }}
                    </NTag>
                  </template>
                </td>
                <td class="nowrap">
                  <NButton text size="tiny" @click="show(r.id)">{{ t('batch.view') }}</NButton>
                  <NButton text size="tiny" style="margin-left: 8px" @click="rerun(r)">{{ t('batch.again') }}</NButton>
                </td>
              </tr>
            </tbody>
          </table>
          <NEmpty v-if="!history.length" class="empty" />
        </div>
      </NTabPane>
    </NTabs>

    <NModal v-model:show="showSave" preset="card" :title="t('batch.saveSnippet')" style="width: 400px; max-width: calc(100vw - 32px)">
      <NInput v-model:value="snippetName" :placeholder="t('settings.snippetName')" maxlength="64" @keyup.enter="saveSnippet" />
      <template #footer>
        <div style="display: flex; justify-content: flex-end; gap: 8px">
          <NButton @click="showSave = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :disabled="!snippetName.trim()" @click="saveSnippet">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>

    <NModal v-model:show="showManage" preset="card" :title="t('batch.manageSnippets')" style="width: 560px; max-width: calc(100vw - 32px)">
      <div class="snips sd-scroll">
        <div v-for="s in snippets" :key="s.id" class="snip">
          <div class="grow">
            <b>{{ s.name }}</b>
            <pre class="sd-mono small">{{ s.command }}</pre>
          </div>
          <NButton text size="tiny" @click="useSnippet(s.id), (showManage = false)">{{ t('batch.use') }}</NButton>
          <NButton text size="tiny" type="error" @click="deleteSnippet(s)">{{ t('common.delete') }}</NButton>
        </div>
        <NEmpty v-if="!snippets.length" :description="t('batch.noSnippets')" />
      </div>
    </NModal>
  </div>
</template>

<style scoped>
.batch {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--sd-surface);
}
.tabs {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.tabs :deep(.n-tabs-nav) {
  padding: 0 12px;
}
.tabs :deep(.n-tabs-pane-wrapper),
.tabs :deep(.n-tab-pane) {
  flex: 1;
  min-height: 0;
  padding: 0;
}
.tabs :deep(.n-tab-pane) {
  height: 100%;
}
.run {
  height: 100%;
  display: flex;
  min-height: 0;
}
.side {
  width: 210px;
  flex: none;
  border-right: 1px solid var(--sd-border);
  padding: 10px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.nodes {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.node {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 3px 4px;
  border-radius: 4px;
  cursor: pointer;
  font-size: 13px;
  min-width: 0;
}
.node:hover {
  background: var(--sd-hover);
}
.dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
  background: #8a8a8a;
  flex: none;
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
.opts {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 6px 8px;
  align-items: center;
  font-size: 12px;
  margin-top: 8px;
}
.main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  padding: 10px 12px;
  gap: 8px;
}
.cmdbar,
.summary {
  display: flex;
  gap: 6px;
  align-items: center;
  margin-top: 6px;
}
.summary {
  margin: 0;
  min-width: 0;
}
.grow {
  flex: 1;
  min-width: 0;
}
.small {
  font-size: 12px;
}
.panes {
  flex: 1;
  min-height: 0;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  grid-auto-rows: minmax(160px, 1fr);
  gap: 8px;
  overflow: auto;
}
.pane {
  display: flex;
  flex-direction: column;
  min-height: 0;
  border: 1px solid var(--sd-border);
  border-radius: var(--sd-radius);
  overflow: hidden;
}
.phead {
  display: flex;
  gap: 6px;
  align-items: center;
  padding: 4px 8px;
  border-top: 3px solid transparent;
  background: var(--sd-surface-2);
  font-size: 13px;
  min-width: 0;
}
.out {
  flex: 1;
  margin: 0;
  padding: 6px 8px;
  background: #1e1e1e;
  color: #d4d4d4;
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
  overflow: auto;
}
.perr {
  color: #f48771;
}
.empty {
  margin: 40px 0;
}
.hist {
  height: 100%;
  padding: 0 12px 12px;
}
.hist table {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
  table-layout: fixed;
}
.hist th {
  text-align: left;
  font-weight: normal;
  color: var(--sd-text-2);
  font-size: 12px;
  padding: 4px 8px;
}
.hist td {
  padding: 5px 8px;
  border-top: 1px solid var(--sd-border);
}
.hist th:nth-child(1) {
  width: 150px;
}
.hist th:nth-child(2) {
  width: 80px;
}
.hist th:nth-child(5) {
  width: 90px;
}
.hist th:nth-child(6) {
  width: 90px;
}
.nowrap {
  white-space: nowrap;
}
.snips {
  max-height: 60vh;
}
.snip {
  display: flex;
  gap: 8px;
  align-items: flex-start;
  padding: 6px 0;
  border-bottom: 1px solid var(--sd-border);
}
.snip pre {
  margin: 4px 0 0;
  white-space: pre-wrap;
  word-break: break-all;
}
@media (max-width: 640px) {
  .run {
    flex-direction: column;
  }
  .side {
    width: auto;
    border-right: 0;
    border-bottom: 1px solid var(--sd-border);
    max-height: 40%;
  }
}
</style>
