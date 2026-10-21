<script setup lang="ts">
// 防火墙：ufw / firewalld 的规则管理。删除规则、添加拒绝规则、启用防火墙需要在 60 秒内确认，否则自动回滚；
// SSH 端口和对外开放的面板端口受保护，不能被删除或拒绝。
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NDataTable, NForm, NFormItem, NInput, NModal, NRadioButton, NRadioGroup, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api } from '@/api/http'
import type { FirewallInfo, FirewallRule, PendingChange, Task } from '@/api/types'
import { useSession } from '@/stores/session'
import { useNotify } from '@/stores/notify'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import PendingBar from '@/components/PendingBar.vue'
import TaskLog from '@/components/TaskLog.vue'
import { errorText } from '@/i18n'

const { t } = useI18n()
const message = useMessage()
const session = useSession()
const notify = useNotify()
const hostInfo = useHost()
const canManage = computed(() => session.has('firewall:manage'))
const base = '/nodes/local/firewall'

const info = ref<FirewallInfo | null>(null)
const loading = ref(false)
const logTask = ref<Task | null>(null)
const st = computed(() => info.value?.status)
const pending = computed<PendingChange | undefined>(() => info.value?.pending)

async function load() {
  loading.value = true
  try {
    info.value = await api.get<FirewallInfo>(base)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

/** 修改后：返回 pending 时显示确认条，否则直接生效 */
function applied(r: { pending: PendingChange | null }) {
  if (!r.pending) message.success(t('common.saved'))
  load()
}

const protectedText = computed(() =>
  Object.entries(info.value?.protected ?? {})
    .map(([p, w]) => `${w} ${p}`)
    .join('、'),
)

function actionTag(a: string) {
  const type = a === 'allow' ? 'success' : a === 'limit' ? 'warning' : 'error'
  return h(NTag, { size: 'small', type, bordered: false }, () => t('firewall.act.' + a))
}

async function removeRule(r: FirewallRule) {
  try {
    const res = await withDanger(
      { level: 1, action: t('firewall.deleteRule'), node: hostInfo.label, impact: [describe(r), r.raw], confirmText: t('common.delete') },
      () => api.post<{ pending: PendingChange | null }>(base + '/rules/delete', { id: r.id }),
    )
    if (res) applied(res)
  } catch (e) {
    message.error(errorText(e))
  }
}

function describe(r: FirewallRule) {
  const what = r.service ? r.service : r.port ? `${r.port}${r.proto ? '/' + r.proto : ''}` : t('firewall.allPorts')
  return `${t('firewall.act.' + r.action)} ${what} ${t('firewall.from')} ${r.source || t('firewall.anywhere')}`
}

const columns = computed<DataTableColumns<FirewallRule>>(() => [
  { title: t('firewall.action'), key: 'action', width: 90, render: (r) => actionTag(r.action) },
  {
    title: t('firewall.port'),
    key: 'port',
    minWidth: 140,
    render: (r) =>
      h('span', { class: 'sd-mono' }, r.service ? t('firewall.service', { name: r.service }) : r.port ? `${r.port}${r.proto ? '/' + r.proto : ''}` : t('firewall.allPorts')),
  },
  { title: t('firewall.source'), key: 'source', minWidth: 140, render: (r) => h('span', { class: 'sd-mono' }, r.source || t('firewall.anywhere')) },
  { title: t('firewall.family'), key: 'family', width: 80, render: (r) => r.family || 'IPv4/6' },
  {
    title: t('firewall.comment'),
    key: 'comment',
    minWidth: 120,
    render: (r) =>
      r.editable ? r.comment || '' : h('span', { class: 'sd-muted sd-mono small', title: r.raw }, t('firewall.readonly') + '：' + r.raw),
  },
  {
    title: '',
    key: 'actions',
    width: 70,
    render: (r) =>
      canManage.value && r.editable
        ? h(NButton, { size: 'tiny', quaternary: true, type: 'error', disabled: !!pending.value, onClick: () => removeRule(r), 'data-testid': 'fw-del-' + r.id }, () => t('common.delete'))
        : null,
  },
])

/* ---------- 添加规则 ---------- */
const showAdd = ref(false)
const form = ref({ action: 'allow', port: '', proto: 'tcp', source: '', comment: '' })
const saving = ref(false)
function openAdd() {
  form.value = { action: 'allow', port: '', proto: 'tcp', source: '', comment: '' }
  showAdd.value = true
}
async function addRule() {
  saving.value = true
  try {
    const r = await api.post<{ pending: PendingChange | null }>(base + '/rules', form.value)
    showAdd.value = false
    applied(r)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

/* ---------- 启用、停用、安装 ---------- */
async function setEnabled(on: boolean) {
  try {
    const res = await withDanger(
      {
        level: 2,
        action: on ? t('firewall.enable') : t('firewall.disable'),
        node: hostInfo.label,
        impact: on ? [t('firewall.enableImpact'), t('firewall.protectedPorts', { ports: protectedText.value })] : t('firewall.disableImpact'),
        confirmText: on ? t('firewall.enable') : t('firewall.disable'),
      },
      (pw) => api.post<{ pending: PendingChange | null }>(base + '/enable', { enabled: on }, { confirmPassword: pw }),
    )
    if (res) applied(res)
  } catch (e) {
    message.error(errorText(e))
  }
}

async function install() {
  try {
    const task = await withDanger(
      { level: 1, action: t('firewall.install'), node: hostInfo.label, impact: t('firewall.installImpact'), confirmText: t('firewall.install') },
      () => api.post<Task>(base + '/install'),
    )
    if (!task) return
    logTask.value = task
    await notify.waitTask(task)
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}
</script>

<template>
  <div class="sd-page firewall" data-testid="firewall">
    <PendingBar v-if="pending" :change="pending" :base="base" :can-manage="canManage" @done="load" />
    <div class="sd-toolbar">
      <template v-if="st && st.backend !== 'none'">
        <b>{{ st.backend }}</b>
        <span class="sd-muted small">{{ st.version }}</span>
        <NTag size="small" :type="st.active ? 'success' : 'warning'" :bordered="false" data-testid="fw-state">
          {{ st.active ? t('firewall.active') : t('firewall.inactive') }}
        </NTag>
        <span v-if="st.zone" class="sd-muted small">{{ t('firewall.zone', { zone: st.zone }) }}</span>
        <span v-if="st.default" class="sd-muted small">{{ t('firewall.default', { policy: st.default }) }}</span>
      </template>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <template v-if="canManage && st && st.backend !== 'none'">
        <NButton v-if="st.active" size="small" :disabled="!!pending" @click="setEnabled(false)">{{ t('firewall.disable') }}</NButton>
        <NButton v-else size="small" type="warning" :disabled="!!pending" data-testid="fw-enable" @click="setEnabled(true)">{{ t('firewall.enable') }}</NButton>
        <NButton size="small" type="primary" :disabled="!!pending" data-testid="fw-add" @click="openAdd">{{ t('firewall.add') }}</NButton>
      </template>
    </div>
    <NAlert v-if="st?.backend === 'none'" type="info" :bordered="false" class="note" :title="t('firewall.none')">
      <p v-for="n in st.notes" :key="n" class="pre">{{ n }}</p>
      <NButton v-if="canManage && !st.managers?.includes('1Panel')" size="small" type="primary" @click="install">{{ t('firewall.install') }}</NButton>
    </NAlert>
    <template v-else-if="st">
      <div v-if="protectedText" class="sd-muted small hint">{{ t('firewall.protectedHint', { ports: protectedText }) }}</div>
      <NAlert v-for="n in st.notes ?? []" :key="n" type="info" :bordered="false" class="note">{{ n }}</NAlert>
      <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="columns" :data="st.rules" :row-key="(r: FirewallRule) => r.id" :bordered="false" :row-props="() => ({ style: '', 'data-testid': 'fw-rule' })" />
      <details v-if="st.raw" class="raw">
        <summary class="small">{{ t('firewall.rawRules', { tool: st.rawTool }) }}</summary>
        <pre class="sd-mono">{{ st.raw }}</pre>
      </details>
    </template>

    <NModal v-model:show="showAdd" preset="card" :title="t('firewall.add')" style="width: 520px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="80" size="small">
        <NFormItem :label="t('firewall.action')">
          <NRadioGroup v-model:value="form.action" size="small">
            <NRadioButton value="allow">{{ t('firewall.act.allow') }}</NRadioButton>
            <NRadioButton value="deny">{{ t('firewall.act.deny') }}</NRadioButton>
            <NRadioButton value="reject">{{ t('firewall.act.reject') }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem :label="t('firewall.port')">
          <NInput v-model:value="form.port" :placeholder="t('firewall.portPh')" data-testid="fw-port" />
        </NFormItem>
        <NFormItem :label="t('firewall.proto')">
          <NRadioGroup v-model:value="form.proto" size="small">
            <NRadioButton value="tcp">TCP</NRadioButton>
            <NRadioButton value="udp">UDP</NRadioButton>
            <NRadioButton value="">{{ t('firewall.both') }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem :label="t('firewall.source')">
          <NInput v-model:value="form.source" :placeholder="t('firewall.sourcePh')" data-testid="fw-source" />
        </NFormItem>
        <NFormItem v-if="st?.backend === 'ufw'" :label="t('firewall.comment')">
          <NInput v-model:value="form.comment" maxlength="64" />
        </NFormItem>
        <div class="sd-muted small">{{ form.action === 'allow' ? t('firewall.allowHint') : t('firewall.denyHint') }}</div>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="showAdd = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="saving" :disabled="!form.port.trim() && !form.source.trim()" data-testid="fw-save" @click="addRule">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>
    <TaskLog v-model:task="logTask" />
  </div>
</template>

<style scoped>
.firewall {
  background: var(--sd-surface);
}
.small,
:deep(.small) {
  font-size: 12px;
}
.hint {
  padding: 0 12px 6px;
}
.note {
  margin: 0 10px 8px;
}
.pre {
  white-space: pre-wrap;
  margin: 0 0 8px;
}
.raw {
  padding: 6px 12px;
}
.raw pre {
  max-height: 200px;
  overflow: auto;
  font-size: 12px;
  background: var(--sd-bg);
  padding: 8px;
  border-radius: 4px;
}
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
</style>
