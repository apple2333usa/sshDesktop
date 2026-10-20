<script setup lang="ts">
// 备份与恢复：备份计划（目录、应用、面板数据，定时执行、加密、保留策略），备份记录与恢复前快照，备份目标。
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NDataTable, NForm, NFormItem, NInput, NInputNumber, NModal, NRadioButton, NRadioGroup, NSelect, NSwitch, NTabPane, NTabs, NTag,
  useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, ApiError, Codes } from '@/api/http'
import { triggerDownload } from '@/api/files'
import type { BackupPlan, BackupRecord, BackupTarget, InstalledApp, Task } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import TaskLog from '@/components/TaskLog.vue'
import ScheduleInput from '@/apps/cron/ScheduleInput.vue'
import { errorText, formatBytes, formatTime } from '@/i18n'
import RestoreDialog from './RestoreDialog.vue'
import TargetsTab from './TargetsTab.vue'
import type { RestoreSource } from './types'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const hostInfo = useHost()
const canManage = computed(() => session.has('backup:manage'))
const base = '/nodes/local/backup'

const tab = ref<string>((host.props.tab as string) || 'plans')
watch(tab, (v) => host.setState({ tab: v }))

const info = ref({ decryptCommand: '', dataDir: '', defaultLocal: '' })
const plans = ref<BackupPlan[]>([])
const targets = ref<BackupTarget[]>([])
const loading = ref(false)
const logTask = ref<Task | null>(null)
const restoring = ref<RestoreSource | null>(null)

async function load() {
  loading.value = true
  try {
    ;[plans.value, targets.value] = await Promise.all([api.get<BackupPlan[]>(base + '/plans'), api.get<BackupTarget[]>(base + '/targets')])
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(async () => {
  load()
  try {
    info.value = await api.get(base + '/info')
  } catch {
    /* 只影响默认路径和提示 */
  }
})

function onTask(task: Task) {
  logTask.value = task
}
// 任务窗口关闭（完成或转到后台）后刷新
watch(logTask, (v) => {
  if (!v) {
    load()
    loadRecords()
  }
})

const typeLabel = (x: string) => t('backup.type.' + x)
const statusType = (s: string) => (({ success: 'success', failed: 'error', running: 'info' }) as Record<string, 'success' | 'error' | 'info'>)[s] ?? 'default'
const targetName = (id: number) => targets.value.find((x) => x.id === id)?.name ?? `#${id}`

/* ---------- 备份计划 ---------- */
async function runPlan(p: BackupPlan) {
  try {
    onTask(await api.post<Task>(`${base}/plans/${p.id}/run`))
  } catch (e) {
    message.error(errorText(e))
  }
}

async function removePlan(p: BackupPlan) {
  try {
    const r = await withDanger(
      { level: 1, action: t('backup.deletePlan'), node: hostInfo.label, impact: [p.name, t('backup.deletePlanImpact')], confirmText: t('common.delete') },
      () => api.del(`${base}/plans/${p.id}`).then(() => true),
    )
    if (r) load()
  } catch (e) {
    message.error(errorText(e))
  }
}

function showRecords(p: BackupPlan) {
  recFilter.value = { plan: p.id, snapshot: false }
  tab.value = 'records'
}

const planCols = computed<DataTableColumns<BackupPlan>>(() => [
  {
    title: t('backup.name'),
    key: 'name',
    minWidth: 160,
    render: (p) =>
      h('div', { class: 'pname' }, [
        h('b', {}, p.name),
        h('div', { class: 'sd-muted small sd-ellipsis sd-mono' }, p.type === 'panel' ? t('backup.panelData') : p.source),
      ]),
  },
  { title: t('backup.typeCol'), key: 'type', width: 80, render: (p) => h(NTag, { size: 'small', bordered: false }, () => typeLabel(p.type)) },
  { title: t('backup.targets'), key: 'targets', minWidth: 120, render: (p) => h('span', { class: 'small' }, p.targetIds.map(targetName).join('，')) },
  {
    title: t('backup.schedule'),
    key: 'schedule',
    width: 120,
    render: (p) => (p.schedule ? h('span', { class: 'sd-mono small' }, p.schedule) : h('span', { class: 'sd-muted small' }, t('backup.manualOnly'))),
  },
  {
    title: t('backup.last'),
    key: 'last',
    width: 170,
    render: (p) =>
      p.last
        ? h('div', { class: 'lrun' }, [
            h(NTag, { size: 'tiny', type: statusType(p.last.status), bordered: false }, () => t('backup.st.' + p.last!.status)),
            h('span', { class: 'small' }, formatTime(p.last.startedAt)),
          ])
        : h('span', { class: 'sd-muted' }, t('backup.never')),
  },
  {
    title: '',
    key: 'actions',
    width: 230,
    render: (p) =>
      h('div', { class: 'acts' }, [
        canManage.value ? h(NButton, { size: 'tiny', secondary: true, onClick: () => runPlan(p), 'data-testid': 'plan-run-' + p.id }, () => t('backup.runNow')) : null,
        h(NButton, { size: 'tiny', quaternary: true, onClick: () => showRecords(p) }, () => t('backup.records')),
        canManage.value ? h(NButton, { size: 'tiny', quaternary: true, onClick: () => openForm(p) }, () => t('common.edit')) : null,
        canManage.value ? h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: () => removePlan(p) }, () => t('common.delete')) : null,
      ]),
  },
])

/* ---------- 新建与修改计划 ---------- */
type Form = Omit<BackupPlan, 'id' | 'hasPassword' | 'last'> & { password: string }
const blank = (): Form => ({
  name: '', type: 'dir', source: '', excludes: '', encrypt: false, password: '', keepCount: 7, keepDays: 0,
  stopApp: false, includeSecretKey: false, targetIds: [], schedule: '0 3 * * *',
})
const showForm = ref(false)
const editing = ref<BackupPlan | null>(null)
const form = ref<Form>(blank())
const saving = ref(false)
const apps = ref<InstalledApp[]>([])

async function openForm(p?: BackupPlan) {
  editing.value = p ?? null
  form.value = p ? { ...p, password: '' } : { ...blank(), targetIds: targets.value.length === 1 ? [targets.value[0].id] : [] }
  showForm.value = true
  if (session.has('appstore:view')) {
    try {
      apps.value = await api.get<InstalledApp[]>('/nodes/local/appstore/installed')
    } catch {
      apps.value = []
    }
  }
}

async function save() {
  saving.value = true
  try {
    if (editing.value) await api.put(`${base}/plans/${editing.value.id}`, form.value)
    else await api.post(base + '/plans', form.value)
    showForm.value = false
    message.success(t('common.saved'))
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

const formOk = computed(() => {
  const f = form.value
  if (!f.name.trim() || !f.targetIds.length) return false
  if (f.type !== 'panel' && !f.source.trim()) return false
  if (f.encrypt && !f.password && !editing.value?.hasPassword) return false
  return !(f.password && f.password.length < 8)
})

/* ---------- 备份记录 ---------- */
const records = ref<BackupRecord[]>([])
const recLoading = ref(false)
const recFilter = ref<{ plan: number | null; snapshot: boolean }>({ plan: null, snapshot: false })

async function loadRecords() {
  recLoading.value = true
  try {
    records.value = await api.get<BackupRecord[]>(base + '/records', {
      plan: recFilter.value.plan ?? undefined,
      snapshot: recFilter.value.snapshot ? '1' : '0',
    })
  } catch (e) {
    message.error(errorText(e))
  } finally {
    recLoading.value = false
  }
}
watch([tab, recFilter], () => tab.value === 'records' && loadRecords(), { deep: true, immediate: true })

function restoreRecord(r: BackupRecord) {
  restoring.value = {
    recordId: r.id,
    key: r.key,
    type: r.type,
    source: r.source,
    encrypted: r.encrypted,
    savedPassword: r.encrypted && !!plans.value.find((p) => p.id === r.planId)?.hasPassword,
    snapshot: r.trigger === 'snapshot',
  }
}

async function removeRecord(r: BackupRecord) {
  const del = (pw?: string, force = false) =>
    api.del(`${base}/records/${r.id}`, { query: { force: force ? '1' : undefined }, confirmPassword: pw }).then(() => true)
  try {
    const ok = await withDanger(
      {
        level: 2,
        action: t('backup.deleteRecord'),
        node: hostInfo.label,
        impact: [`${r.targetName || t('backup.snapshots')}：${r.key || '-'}`, r.status === 'success' ? t('backup.deleteRecordImpact') : t('backup.deleteRecordOnly')],
        confirmText: t('common.delete'),
      },
      async (pw) => {
        try {
          return await del(pw)
        } catch (e) {
          if (e instanceof ApiError && e.code === Codes.ConfirmFailed) throw e
          // 备份文件已经不在或目标连不上时，可以只删除记录
          const ok2 = await withDanger(
            { level: 1, action: t('backup.forceDelete'), node: hostInfo.label, impact: [errorText(e), t('backup.forceDeleteImpact')], confirmText: t('backup.forceDelete') },
            () => del(pw, true),
          )
          return !!ok2
        }
      },
    )
    if (ok) loadRecords()
  } catch (e) {
    message.error(errorText(e))
  }
}

const recCols = computed<DataTableColumns<BackupRecord>>(() => [
  { title: t('backup.time'), key: 'startedAt', width: 150, render: (r) => formatTime(r.startedAt) },
  {
    title: t('backup.content'),
    key: 'source',
    minWidth: 180,
    render: (r) =>
      h('div', { class: 'pname' }, [
        h('div', {}, [h('b', {}, r.planName || typeLabel(r.type)), h('span', { class: 'sd-muted small' }, '　' + typeLabel(r.type))]),
        h('div', { class: 'sd-muted small sd-ellipsis sd-mono', title: r.key }, r.type === 'panel' ? t('backup.panelData') : r.source),
      ]),
  },
  { title: t('backup.target'), key: 'targetName', width: 120, render: (r) => r.targetName || (r.targetId ? `#${r.targetId}` : t('backup.localSnapshot')) },
  {
    title: t('backup.size'),
    key: 'size',
    width: 110,
    render: (r) =>
      h('div', { class: 'lrun' }, [
        h('span', { class: 'small' }, r.size ? formatBytes(r.size) : '-'),
        r.encrypted ? h(NTag, { size: 'tiny', bordered: false, type: 'warning' }, () => t('backup.encrypted')) : null,
      ]),
  },
  {
    title: t('backup.status'),
    key: 'status',
    width: 120,
    render: (r) =>
      h('div', { class: 'lrun', title: r.error }, [
        h(NTag, { size: 'tiny', type: statusType(r.status), bordered: false }, () => t('backup.st.' + r.status)),
        h('span', { class: 'sd-muted small' }, t('backup.trigger.' + r.trigger)),
      ]),
  },
  {
    title: '',
    key: 'actions',
    width: 190,
    render: (r) =>
      canManage.value
        ? h('div', { class: 'acts' }, [
            r.status === 'success' ? h(NButton, { size: 'tiny', secondary: true, onClick: () => restoreRecord(r), 'data-testid': 'rec-restore-' + r.id }, () => t('backup.restore')) : null,
            r.status === 'success'
              ? h(NButton, { size: 'tiny', quaternary: true, onClick: () => triggerDownload(`/api/v1${base}/records/${r.id}/download`) }, () => t('explorer.download'))
              : null,
            r.status !== 'running' ? h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: () => removeRecord(r) }, () => t('common.delete')) : null,
          ])
        : null,
  },
])
</script>

<template>
  <div class="sd-page backup" data-testid="backup">
    <NTabs v-model:value="tab" type="line" size="small" class="tabs">
      <NTabPane name="plans" :tab="t('backup.plans')">
        <NAlert v-if="!loading && !targets.length && canManage" type="info" :bordered="false" class="warn">
          {{ t('backup.noTargetHint') }}
          <NButton size="tiny" type="primary" style="margin-left: 8px" @click="tab = 'targets'">{{ t('backup.addTarget') }}</NButton>
        </NAlert>
        <div class="sd-toolbar">
          <span class="sd-muted small">{{ t('backup.plansHint') }}</span>
          <span style="flex: 1" />
          <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
          <NButton v-if="canManage" size="small" type="primary" :disabled="!targets.length" data-testid="plan-add" @click="openForm()">{{ t('backup.addPlan') }}</NButton>
        </div>
        <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="planCols" :data="plans" :row-key="(p: BackupPlan) => p.id" :bordered="false" :row-props="(p: BackupPlan) => ({ style: '', 'data-testid': 'plan-' + p.id })" />
      </NTabPane>
      <NTabPane name="records" :tab="t('backup.records')">
        <div class="sd-toolbar">
          <NRadioGroup v-model:value="recFilter.snapshot" size="small">
            <NRadioButton :value="false">{{ t('backup.records') }}</NRadioButton>
            <NRadioButton :value="true" data-testid="rec-snapshots">{{ t('backup.snapshots') }}</NRadioButton>
          </NRadioGroup>
          <NSelect
            v-if="!recFilter.snapshot"
            v-model:value="recFilter.plan"
            size="small"
            clearable
            :placeholder="t('backup.allPlans')"
            :options="plans.map((p) => ({ label: p.name, value: p.id }))"
            style="width: 180px"
          />
          <span style="flex: 1" />
          <NButton size="small" quaternary @click="loadRecords">{{ t('explorer.refresh') }}</NButton>
        </div>
        <div class="sd-muted small hintbar">
          {{ recFilter.snapshot ? t('backup.snapshotsHint') : t('backup.recordsHint') }}
          <template v-if="!recFilter.snapshot && info.decryptCommand">
            <br />{{ t('backup.decryptHint') }}<code class="sd-mono">{{ info.decryptCommand }}</code>
          </template>
        </div>
        <NDataTable class="sd-fill" size="small" flex-height :loading="recLoading" :columns="recCols" :data="records" :row-key="(r: BackupRecord) => r.id" :bordered="false" :row-props="(r: BackupRecord) => ({ style: '', 'data-testid': 'rec-' + r.id })" />
      </NTabPane>
      <NTabPane name="targets" :tab="t('backup.targets')" display-directive="show:lazy">
        <TargetsTab :default-local="info.defaultLocal" @restore="(s) => (restoring = s)" @changed="load" />
      </NTabPane>
    </NTabs>

    <NModal v-model:show="showForm" preset="card" :title="editing ? t('backup.editPlan') : t('backup.addPlan')" style="width: 640px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="104" size="small" class="form sd-scroll">
        <NFormItem :label="t('backup.typeCol')">
          <NRadioGroup v-model:value="form.type" size="small">
            <NRadioButton value="dir">{{ typeLabel('dir') }}</NRadioButton>
            <NRadioButton value="app" :disabled="!apps.length">{{ typeLabel('app') }}</NRadioButton>
            <NRadioButton value="panel">{{ typeLabel('panel') }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem :label="t('backup.name')" required>
          <NInput v-model:value="form.name" maxlength="64" data-testid="plan-name" />
        </NFormItem>
        <NFormItem v-if="form.type === 'dir'" :label="t('backup.dir')" required>
          <NInput v-model:value="form.source" class="sd-mono" placeholder="/var/www" data-testid="plan-source" />
        </NFormItem>
        <NFormItem v-else-if="form.type === 'app'" :label="t('backup.app')" required>
          <NSelect v-model:value="form.source" :options="apps.map((a) => ({ label: `${a.title}（${a.name}）`, value: a.name }))" />
        </NFormItem>
        <NAlert v-else type="info" :bordered="false" class="falert">{{ t('backup.panelHint') }}</NAlert>
        <NFormItem v-if="form.type !== 'panel'" :label="t('backup.excludes')">
          <div class="col">
            <NInput v-model:value="form.excludes" type="textarea" class="code" :autosize="{ minRows: 2, maxRows: 6 }" placeholder="node_modules&#10;*.log&#10;cache/" />
            <span class="sd-muted small">{{ t('backup.excludesHint') }}</span>
          </div>
        </NFormItem>
        <NFormItem v-if="form.type === 'app'" :label="t('backup.stopApp')">
          <NSwitch v-model:value="form.stopApp" />
          <span class="sd-muted small" style="margin-left: 8px">{{ t('backup.stopAppHint') }}</span>
        </NFormItem>
        <NFormItem v-if="form.type === 'panel'" :label="t('backup.includeKey')">
          <div class="col">
            <NSwitch v-model:value="form.includeSecretKey" />
            <span class="sd-muted small">{{ form.includeSecretKey ? t('backup.includeKeyOn') : t('backup.includeKeyOff') }}</span>
          </div>
        </NFormItem>
        <NFormItem :label="t('backup.targets')" required>
          <NSelect v-model:value="form.targetIds" multiple :options="targets.map((x) => ({ label: x.name, value: x.id }))" data-testid="plan-targets" />
        </NFormItem>
        <NFormItem :label="t('backup.schedule')">
          <div class="col">
            <ScheduleInput v-model:value="form.schedule" />
            <span class="sd-muted small">{{ t('backup.scheduleHint') }}</span>
          </div>
        </NFormItem>
        <NFormItem :label="t('backup.keep')">
          <div class="keep">
            <NInputNumber v-model:value="form.keepCount" :min="0" :max="10000" style="width: 130px">
              <template #suffix>{{ t('backup.copies') }}</template>
            </NInputNumber>
            <NInputNumber v-model:value="form.keepDays" :min="0" :max="36500" style="width: 130px">
              <template #suffix>{{ t('settings.days') }}</template>
            </NInputNumber>
          </div>
        </NFormItem>
        <div class="sd-muted small hint">{{ t('backup.keepHint') }}</div>
        <NFormItem :label="t('backup.encrypt')">
          <NSwitch v-model:value="form.encrypt" data-testid="plan-encrypt" />
        </NFormItem>
        <NFormItem v-if="form.encrypt" :label="t('backup.password')" :required="!editing?.hasPassword">
          <div class="col">
            <NInput
              v-model:value="form.password"
              type="password"
              show-password-on="click"
              :placeholder="editing?.hasPassword ? t('backup.passwordSaved') : t('backup.passwordMin')"
              :input-props="{ autocomplete: 'new-password' }"
              data-testid="plan-password"
            />
            <span class="sd-muted small">{{ t('backup.encryptHint') }}</span>
          </div>
        </NFormItem>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="showForm = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="saving" :disabled="!formOk" data-testid="plan-save" @click="save">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>

    <RestoreDialog :source="restoring" @close="restoring = null" @task="onTask" />
    <TaskLog v-model:task="logTask" />
  </div>
</template>

<style scoped>
.backup {
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
.small,
:deep(.small) {
  font-size: 12px;
}
:deep(.pname) {
  min-width: 0;
}
:deep(.lrun),
:deep(.acts) {
  display: flex;
  gap: 6px;
  align-items: center;
}
.hintbar {
  padding: 0 12px 6px;
}
.hintbar code {
  user-select: all;
  word-break: break-all;
}
.form {
  max-height: 66vh;
  padding-right: 6px;
}
.falert {
  margin-bottom: 12px;
}
.col {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
}
.keep {
  display: flex;
  gap: 8px;
}
.hint {
  margin: -8px 0 12px 104px;
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
</style>
