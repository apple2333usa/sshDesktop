<script setup lang="ts">
// 计划任务：面板任务（Shell 脚本、备份、访问 URL、清理日志，由系统 cron 调用面板执行并记录输出），
// 以及系统已有的 crontab 和 systemd 定时器。
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NInputNumber, NModal, NRadioButton, NRadioGroup,
  NSelect, NSwitch, NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api } from '@/api/http'
import type { BackupPlan, CronJob, CronRun, CronStatus, CronType, SystemdTimer, Task } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useNotify } from '@/stores/notify'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import TaskLog from '@/components/TaskLog.vue'
import { errorText, formatTime } from '@/i18n'
import ScheduleInput from './ScheduleInput.vue'
import SystemCrontab from './SystemCrontab.vue'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const notify = useNotify()
const hostInfo = useHost()
const canManage = computed(() => session.has('cron:manage'))
const base = '/nodes/local/cron'

const tab = ref<string>((host.props.tab as string) || 'jobs')
watch(tab, (v) => host.setState({ tab: v }))

const status = ref<CronStatus | null>(null)
const jobs = ref<CronJob[]>([])
const plans = ref<BackupPlan[]>([])
const loading = ref(false)
const logTask = ref<Task | null>(null)

async function load() {
  loading.value = true
  try {
    ;[status.value, jobs.value] = await Promise.all([api.get<CronStatus>(base + '/status'), api.get<CronJob[]>(base + '/jobs')])
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
async function loadPlans() {
  if (!session.has('backup:view')) return
  try {
    plans.value = await api.get<BackupPlan[]>('/nodes/local/backup/plans')
  } catch {
    plans.value = []
  }
}
onMounted(() => {
  load()
  loadPlans()
})

async function installDaemon() {
  try {
    const task = await withDanger(
      { level: 1, action: t('cron.installDaemon'), node: hostInfo.label, impact: t('cron.installDaemonImpact'), confirmText: t('cron.install') },
      () => api.post<Task>(base + '/install-daemon'),
    )
    if (!task) return
    logTask.value = task
    await notify.waitTask(task)
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

/* ---------- 列表 ---------- */
const typeLabel = (x: CronType) => t('cron.type.' + x)
const statusType = (s: string) => (({ success: 'success', failed: 'error', skipped: 'warning', running: 'info' }) as Record<string, 'success' | 'error' | 'warning' | 'info'>)[s] ?? 'default'

function summary(j: CronJob) {
  switch (j.type) {
    case 'shell':
      return j.script.split('\n').find((l) => l.trim() && !l.startsWith('#')) ?? ''
    case 'url':
      return j.url
    case 'cleanup':
      return `${j.cleanDirs.split('\n').filter(Boolean).join('，')}（${j.cleanPattern || '*.log*'}，${t('cron.olderThan', { n: j.cleanDays })}）`
    case 'backup':
      return plans.value.find((p) => p.id === j.planId)?.name ?? `#${j.planId}`
  }
  return ''
}

async function toggle(j: CronJob, enabled: boolean) {
  try {
    await api.post(`${base}/jobs/${j.id}/enable`, { enabled })
    j.enabled = enabled
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function runNow(j: CronJob) {
  try {
    const run = await api.post<CronRun>(`${base}/jobs/${j.id}/run`)
    message.info(t('cron.started'))
    openRuns(j, run.id)
  } catch (e) {
    message.error(errorText(e))
  }
}

async function remove(j: CronJob) {
  try {
    const r = await withDanger(
      { level: 1, action: t('cron.deleteJob'), node: hostInfo.label, impact: [j.name, j.schedule], confirmText: t('common.delete') },
      () => api.del(`${base}/jobs/${j.id}`).then(() => true),
    )
    if (r) load()
  } catch (e) {
    message.error(errorText(e))
  }
}

const columns = computed<DataTableColumns<CronJob>>(() => [
  {
    title: t('cron.name'),
    key: 'name',
    minWidth: 160,
    render: (j) =>
      h('div', { class: 'jname' }, [
        h('b', {}, j.name),
        h('div', { class: 'sd-muted small sd-ellipsis sd-mono' }, summary(j)),
      ]),
  },
  { title: t('cron.typeCol'), key: 'type', width: 90, render: (j) => h(NTag, { size: 'small', bordered: false }, () => typeLabel(j.type)) },
  {
    title: t('cron.schedule'),
    key: 'schedule',
    width: 190,
    render: (j) =>
      h('div', {}, [
        h('div', { class: 'sd-mono' }, j.schedule),
        h('div', { class: 'sd-muted small' }, j.enabled && j.next ? t('cron.next', { time: formatTime(j.next * 1000) }) : j.enabled ? '' : t('cron.disabled')),
      ]),
  },
  {
    title: t('cron.lastRun'),
    key: 'lastRunAt',
    width: 170,
    render: (j) =>
      j.lastRunAt
        ? h('div', { class: 'lrun' }, [h(NTag, { size: 'tiny', type: statusType(j.lastStatus), bordered: false }, () => t('cron.st.' + j.lastStatus)), h('span', { class: 'small' }, formatTime(j.lastRunAt))])
        : h('span', { class: 'sd-muted' }, t('cron.never')),
  },
  {
    title: t('cron.enabled'),
    key: 'enabled',
    width: 70,
    render: (j) => h(NSwitch, { size: 'small', value: j.enabled, disabled: !canManage.value, 'onUpdate:value': (v: boolean) => toggle(j, v) }),
  },
  {
    title: '',
    key: 'actions',
    width: 220,
    render: (j) =>
      h('div', { class: 'acts' }, [
        canManage.value ? h(NButton, { size: 'tiny', secondary: true, onClick: () => runNow(j), 'data-testid': 'cron-run-' + j.id }, () => t('cron.runNow')) : null,
        h(NButton, { size: 'tiny', quaternary: true, onClick: () => openRuns(j), 'data-testid': 'cron-runs-' + j.id }, () => t('cron.history')),
        canManage.value ? h(NButton, { size: 'tiny', quaternary: true, onClick: () => openForm(j) }, () => t('common.edit')) : null,
        canManage.value ? h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: () => remove(j) }, () => t('common.delete')) : null,
      ]),
  },
])

/* ---------- 新建与修改 ---------- */
type Form = Omit<CronJob, 'id' | 'lastRunAt' | 'lastStatus' | 'next'>
const blank = (): Form => ({
  name: '', type: 'shell', schedule: '0 3 * * *', enabled: true, script: '#!/bin/bash\n', url: '', planId: 0,
  cleanDirs: '', cleanPattern: '*.log*', cleanDays: 7, timeout: 0, keepRuns: 30,
})
const showForm = ref(false)
const editing = ref<number | null>(null)
const form = ref<Form>(blank())
const saving = ref(false)

function openForm(j?: CronJob) {
  editing.value = j?.id ?? null
  form.value = j ? { ...j } : blank()
  showForm.value = true
}

async function save() {
  saving.value = true
  try {
    if (editing.value) await api.put(`${base}/jobs/${editing.value}`, form.value)
    else await api.post(`${base}/jobs`, form.value)
    showForm.value = false
    message.success(t('common.saved'))
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

/* ---------- 执行记录 ---------- */
const runsOf = ref<CronJob | null>(null)
const runs = ref<CronRun[]>([])
const runDetail = ref<CronRun | null>(null)
let poll: number | undefined

async function openRuns(j: CronJob, focus?: number) {
  runsOf.value = j
  runDetail.value = null
  await loadRuns()
  const id = focus ?? runs.value[0]?.id
  if (id) showRun(id)
}
async function loadRuns() {
  if (!runsOf.value) return
  try {
    runs.value = await api.get<CronRun[]>(`${base}/jobs/${runsOf.value.id}/runs`)
  } catch (e) {
    message.error(errorText(e))
  }
}
async function showRun(id: number) {
  clearTimeout(poll)
  try {
    runDetail.value = await api.get<CronRun>(`${base}/runs/${id}`)
    // 正在执行时每秒刷新输出
    if (runDetail.value.status === 'running' && runsOf.value) poll = window.setTimeout(() => showRun(id), 1000)
    else {
      loadRuns()
      load()
    }
  } catch (e) {
    message.error(errorText(e))
  }
}
watch(runsOf, (v) => !v && clearTimeout(poll))

/* ---------- systemd 定时器 ---------- */
const timers = ref<SystemdTimer[]>([])
async function loadTimers() {
  try {
    timers.value = await api.get<SystemdTimer[]>(base + '/timers')
  } catch (e) {
    message.error(errorText(e))
  }
}
watch(tab, (v) => v === 'timers' && loadTimers(), { immediate: true })
const timerCols = computed<DataTableColumns<SystemdTimer>>(() => [
  { title: t('cron.timer'), key: 'unit', minWidth: 200, render: (x) => h('span', { class: 'sd-mono' }, x.unit) },
  { title: t('cron.activates'), key: 'activates', minWidth: 200, render: (x) => h('span', { class: 'sd-mono' }, x.activates) },
  { title: t('cron.nextRun'), key: 'next', width: 170, sorter: (a, b) => (a.next || Infinity) - (b.next || Infinity), render: (x) => (x.next ? formatTime(x.next * 1000) : '-') },
  { title: t('cron.lastRun'), key: 'last', width: 170, render: (x) => (x.last ? formatTime(x.last * 1000) : '-') },
])
</script>

<template>
  <div class="sd-page cron" data-testid="cron">
    <NAlert v-if="status && !status.running" type="warning" :bordered="false" class="warn">
      {{ status.installed ? t('cron.daemonStopped', { name: status.daemon }) : t('cron.daemonMissing') }}
      <NButton v-if="canManage && !status.installed" size="tiny" type="primary" style="margin-left: 8px" @click="installDaemon">{{ t('cron.install') }}</NButton>
    </NAlert>
    <NTabs v-model:value="tab" type="line" size="small" class="tabs">
      <template #suffix>
        <span v-if="status" class="sd-muted small tz">{{ t('cron.timezone', { tz: status.timezone }) }}</span>
      </template>
      <NTabPane name="jobs" :tab="t('cron.panelJobs')">
        <div class="sd-toolbar">
          <span class="sd-muted small">{{ t('cron.jobsHint') }}</span>
          <span style="flex: 1" />
          <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
          <NButton v-if="canManage" size="small" type="primary" data-testid="cron-add" @click="openForm()">{{ t('cron.add') }}</NButton>
        </div>
        <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="columns" :data="jobs" :row-key="(j: CronJob) => j.id" :bordered="false" :row-props="(j: CronJob) => ({ style: '', 'data-testid': 'cron-job-' + j.id })" />
      </NTabPane>
      <NTabPane name="system" :tab="t('cron.systemCrontab')" display-directive="show:lazy">
        <SystemCrontab />
      </NTabPane>
      <NTabPane name="timers" :tab="t('cron.timers')">
        <div class="sd-toolbar">
          <span class="sd-muted small">{{ t('cron.timersHint') }}</span>
          <span style="flex: 1" />
          <NButton size="small" quaternary @click="loadTimers">{{ t('explorer.refresh') }}</NButton>
        </div>
        <NDataTable class="sd-fill" size="small" flex-height :columns="timerCols" :data="timers" :row-key="(x: SystemdTimer) => x.unit" :bordered="false" />
      </NTabPane>
    </NTabs>

    <NModal v-model:show="showForm" preset="card" :title="editing ? t('cron.edit') : t('cron.add')" style="width: 620px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="96" size="small" class="form sd-scroll">
        <NFormItem :label="t('cron.typeCol')">
          <NRadioGroup v-model:value="form.type" size="small">
            <NRadioButton v-for="x in ['shell', 'backup', 'url', 'cleanup']" :key="x" :value="x" :disabled="x === 'backup' && !plans.length">{{ typeLabel(x as CronType) }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem :label="t('cron.name')" required>
          <NInput v-model:value="form.name" maxlength="64" data-testid="cron-name" />
        </NFormItem>
        <NFormItem :label="t('cron.schedule')" required>
          <ScheduleInput v-model:value="form.schedule" />
        </NFormItem>
        <NFormItem v-if="form.type === 'shell'" :label="t('cron.script')">
          <NInput v-model:value="form.script" type="textarea" class="code" :autosize="{ minRows: 6, maxRows: 16 }" data-testid="cron-script" />
        </NFormItem>
        <NFormItem v-else-if="form.type === 'url'" label="URL">
          <NInput v-model:value="form.url" placeholder="https://example.com/cron.php" />
        </NFormItem>
        <NFormItem v-else-if="form.type === 'backup'" :label="t('cron.plan')">
          <NSelect v-model:value="form.planId" :options="plans.map((p) => ({ label: p.name, value: p.id }))" />
        </NFormItem>
        <template v-else>
          <NFormItem :label="t('cron.cleanDirs')">
            <NInput v-model:value="form.cleanDirs" type="textarea" class="code" :autosize="{ minRows: 2, maxRows: 6 }" placeholder="/var/log/nginx" />
          </NFormItem>
          <NFormItem :label="t('cron.cleanPattern')">
            <NInput v-model:value="form.cleanPattern" placeholder="*.log*" style="width: 200px" />
          </NFormItem>
          <NFormItem :label="t('cron.cleanDays')">
            <NInputNumber v-model:value="form.cleanDays" :min="0" style="width: 140px">
              <template #suffix>{{ t('settings.days') }}</template>
            </NInputNumber>
          </NFormItem>
        </template>
        <NFormItem :label="t('cron.timeout')">
          <NInputNumber v-model:value="form.timeout" :min="0" style="width: 160px">
            <template #suffix>s</template>
          </NInputNumber>
          <span class="sd-muted small" style="margin-left: 8px">{{ t('cron.timeoutHint') }}</span>
        </NFormItem>
        <NFormItem :label="t('cron.keepRuns')">
          <NInputNumber v-model:value="form.keepRuns" :min="1" :max="1000" style="width: 160px" />
        </NFormItem>
        <NFormItem :label="t('cron.enabled')">
          <NSwitch v-model:value="form.enabled" />
        </NFormItem>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="showForm = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="saving" :disabled="!form.name.trim() || !form.schedule.trim()" data-testid="cron-save" @click="save">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>

    <NDrawer :show="!!runsOf" :width="720" placement="right" @update:show="(v: boolean) => !v && (runsOf = null)">
      <NDrawerContent :title="t('cron.historyOf', { name: runsOf?.name ?? '' })" closable>
        <div class="runs">
          <div class="rlist sd-scroll">
            <div v-for="r in runs" :key="r.id" class="ritem" :class="{ sel: runDetail?.id === r.id }" @click="showRun(r.id)">
              <NTag size="tiny" :type="statusType(r.status)" :bordered="false">{{ t('cron.st.' + r.status) }}</NTag>
              <span class="small">{{ formatTime(r.startedAt, true) }}</span>
              <span class="sd-muted small">{{ t('cron.trigger.' + r.trigger) }}</span>
            </div>
            <div v-if="!runs.length" class="sd-muted small">{{ t('cron.noRuns') }}</div>
          </div>
          <div class="rdetail">
            <template v-if="runDetail">
              <div class="small sd-muted">
                {{ t('cron.exitCode') }} {{ runDetail.exitCode }} · {{ t('cron.duration', { s: (runDetail.durationMs / 1000).toFixed(1) }) }}
              </div>
              <pre class="out sd-mono" data-testid="cron-output">{{ runDetail.output || t('cron.noOutput') }}</pre>
            </template>
          </div>
        </div>
      </NDrawerContent>
    </NDrawer>
    <TaskLog v-model:task="logTask" />
  </div>
</template>

<style scoped>
.cron {
  background: var(--sd-surface);
}
.warn {
  margin: 8px 10px 0;
}
.tabs {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.tabs :deep(.n-tabs-nav) {
  padding-left: 8px;
}
.tabs :deep(.n-tabs-pane-wrapper) {
  flex: 1;
  min-height: 0;
}
.tabs :deep(.n-tab-pane) {
  height: 100%;
  padding: 0;
  display: flex;
  flex-direction: column;
}
.tz {
  padding-right: 12px;
}
.small,
:deep(.small) {
  font-size: 12px;
}
:deep(.jname) {
  min-width: 0;
}
:deep(.lrun),
:deep(.acts) {
  display: flex;
  gap: 6px;
  align-items: center;
}
.form {
  max-height: 64vh;
  padding-right: 6px;
}
.code :deep(textarea) {
  font-family: var(--sd-mono);
  font-size: 12px;
}
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
.runs {
  display: flex;
  gap: 10px;
  height: 100%;
}
.rlist {
  width: 220px;
  flex: none;
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.ritem {
  display: flex;
  gap: 6px;
  align-items: center;
  padding: 5px 6px;
  border-radius: 4px;
  cursor: pointer;
}
.ritem:hover {
  background: var(--sd-hover);
}
.ritem.sel {
  background: var(--sd-active);
}
.rdetail {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.out {
  flex: 1;
  margin: 0;
  overflow: auto;
  font-size: 12px;
  padding: 8px;
  border-radius: 4px;
  background: var(--sd-bg);
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
