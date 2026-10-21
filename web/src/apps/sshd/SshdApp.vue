<script setup lang="ts">
// SSH 设置：端口、密码登录、root 登录、公钥登录（修改后 60 秒内确认，否则回滚；改端口时新旧端口同时可用），
// 各账号的授权密钥，以及 SSH 登录记录。
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NDataTable, NDescriptions, NDescriptionsItem, NForm, NFormItem, NInput, NInputNumber, NModal, NSelect, NSwitch,
  NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api } from '@/api/http'
import type { PendingChange, SshKey, SshLogin, SshdStatus, SysUser } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import PendingBar from '@/components/PendingBar.vue'
import { errorText } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const hostInfo = useHost()
const canManage = computed(() => session.has('sshd:manage'))
const base = '/nodes/local/sshd'

const tab = ref<string>((host.props.tab as string) || 'settings')
watch(tab, (v) => host.setState({ tab: v }))

/* ---------- 设置 ---------- */
const status = ref<SshdStatus | null>(null)
const pending = ref<PendingChange | null>(null)
const form = ref({ port: 22, passwordAuth: true, permitRootLogin: 'prohibit-password', pubkeyAuth: true })
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    const r = await api.get<{ status: SshdStatus; pending?: PendingChange }>(base)
    status.value = r.status
    pending.value = r.pending ?? null
    const e = r.status.effective
    if (e) {
      // 等待确认时端口有新旧两个，表单显示新端口
      const next = (r.pending?.extra as { next?: typeof form.value } | undefined)?.next
      form.value = next ? { ...next } : { port: e.ports[0] ?? 22, passwordAuth: e.passwordAuth, permitRootLogin: e.permitRootLogin, pubkeyAuth: e.pubkeyAuth }
    }
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const rootOptions = computed(() => ['prohibit-password', 'yes', 'no'].map((v) => ({ value: v, label: t('sshd.root.' + v) })))

async function save() {
  const e = status.value?.effective
  const impact: string[] = []
  if (e && (e.ports.length !== 1 || e.ports[0] !== form.value.port)) impact.push(t('sshd.portImpact', { old: e.ports.join(', '), port: form.value.port }))
  if (!form.value.passwordAuth) impact.push(t('sshd.noPasswordImpact'))
  if (form.value.permitRootLogin === 'no') impact.push(t('sshd.noRootImpact'))
  impact.push(t('sshd.confirmImpact'))
  try {
    const r = await withDanger(
      { level: 2, action: t('sshd.save'), node: hostInfo.label, impact, confirmText: t('sshd.save') },
      (pw) => api.put<{ pending: PendingChange }>(base + '/config', form.value, { confirmPassword: pw }),
    )
    if (r) load()
  } catch (err) {
    message.error(errorText(err))
  }
}

/* ---------- 授权密钥 ---------- */
const users = ref<SysUser[]>([])
const keyUser = ref('root')
const keys = ref<SshKey[]>([])
const keyPath = ref('')
async function loadKeys() {
  try {
    const r = await api.get<{ users: SysUser[]; user?: string; path?: string; keys: SshKey[] }>(base + '/keys', { user: keyUser.value })
    users.value = r.users
    keys.value = r.keys
    keyPath.value = r.path ?? ''
  } catch (e) {
    message.error(errorText(e))
  }
}
watch(tab, (v) => v === 'keys' && loadKeys(), { immediate: true })
watch(keyUser, loadKeys)

const showKey = ref(false)
const newKey = ref('')
async function addKey() {
  try {
    await api.post(base + '/keys', { user: keyUser.value, key: newKey.value })
    showKey.value = false
    newKey.value = ''
    message.success(t('common.saved'))
    loadKeys()
  } catch (e) {
    message.error(errorText(e))
  }
}
async function removeKey(k: SshKey) {
  try {
    const r = await withDanger(
      { level: 2, action: t('sshd.deleteKey'), node: hostInfo.label, impact: [`${keyUser.value}：${k.type} ${k.comment}`, k.fingerprint || t('sshd.badKey'), t('sshd.deleteKeyImpact')], confirmText: t('common.delete') },
      (pw) => api.post(base + '/keys/delete', { user: keyUser.value, line: k.line, fingerprint: k.fingerprint }, { confirmPassword: pw }).then(() => true),
    )
    if (r) loadKeys()
  } catch (e) {
    message.error(errorText(e))
  }
}
const keyCols = computed<DataTableColumns<SshKey>>(() => [
  { title: t('sshd.keyType'), key: 'type', width: 130, render: (k) => (k.error ? h(NTag, { size: 'small', type: 'error', bordered: false }, () => t('sshd.badKey')) : k.type) },
  { title: t('sshd.keyComment'), key: 'comment', minWidth: 140 },
  { title: t('sshd.fingerprint'), key: 'fingerprint', minWidth: 220, render: (k) => h('span', { class: 'sd-mono small' }, k.fingerprint) },
  { title: t('sshd.options'), key: 'options', width: 120, render: (k) => h('span', { class: 'sd-mono small' }, k.options ?? '') },
  {
    title: '',
    key: 'a',
    width: 70,
    render: (k) => (canManage.value ? h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: () => removeKey(k) }, () => t('common.delete')) : null),
  },
])

/* ---------- 登录记录 ---------- */
interface LoginStats {
  source: string
  items: SshLogin[]
  success: number
  failed: number
  topFailed: { ip: string; failed: number; users: number }[]
}
const logins = ref<LoginStats | null>(null)
const onlyFailed = ref(false)
async function loadLogins() {
  try {
    logins.value = await api.get<LoginStats>(base + '/logins')
  } catch (e) {
    message.error(errorText(e))
  }
}
watch(tab, (v) => v === 'logins' && loadLogins(), { immediate: true })
const loginRows = computed(() => (logins.value?.items ?? []).filter((l) => !onlyFailed.value || !l.success))
const loginCols = computed<DataTableColumns<SshLogin>>(() => [
  { title: t('sshd.time'), key: 'time', width: 170, render: (l) => l.time.replace('T', ' ') },
  {
    title: t('sshd.result'),
    key: 'success',
    width: 80,
    render: (l) => h(NTag, { size: 'small', type: l.success ? 'success' : 'error', bordered: false }, () => (l.success ? t('sshd.ok') : t('sshd.fail'))),
  },
  { title: t('sshd.user'), key: 'user', width: 130, render: (l) => (l.invalid ? h('span', { class: 'sd-muted' }, l.user + t('sshd.invalidUser')) : l.user) },
  { title: 'IP', key: 'ip', minWidth: 140, render: (l) => h('span', { class: 'sd-mono' }, l.ip) },
  { title: t('sshd.method'), key: 'method', width: 110 },
])
</script>

<template>
  <div class="sd-page sshd" data-testid="sshd">
    <PendingBar v-if="pending" :change="pending" :base="base" :can-manage="canManage" @done="load" />
    <NTabs v-model:value="tab" type="line" size="small" class="tabs">
      <NTabPane name="settings" :tab="t('sshd.settings')">
        <div class="body sd-scroll">
          <NAlert v-if="status && !status.installed" type="warning" :bordered="false">{{ t('sshd.notInstalled') }}</NAlert>
          <NAlert v-if="status?.error" type="error" :bordered="false">{{ status.error }}</NAlert>
          <template v-if="status?.installed">
            <NDescriptions size="small" :column="2" label-placement="left" bordered class="desc">
              <NDescriptionsItem :label="t('sshd.version')">{{ status.version }}</NDescriptionsItem>
              <NDescriptionsItem :label="t('sshd.service')">
                {{ status.service }}{{ status.socket ? ' (ssh.socket)' : '' }}
                <NTag size="tiny" :type="status.active ? 'success' : 'warning'" :bordered="false">{{ status.active ? t('sshd.running') : t('sshd.stopped') }}</NTag>
              </NDescriptionsItem>
              <NDescriptionsItem :label="t('sshd.listen')">
                <span class="sd-mono">{{ status.effective?.listenAddresses.join('  ') }}</span>
              </NDescriptionsItem>
              <NDescriptionsItem :label="t('sshd.files')">
                <span class="sd-mono small">{{ status.files.join('、') }}</span>
              </NDescriptionsItem>
            </NDescriptions>
            <NAlert v-for="n in status.notes" :key="n" type="info" :bordered="false" class="note">{{ n }}</NAlert>
            <NForm label-placement="left" label-width="120" size="small" class="form">
              <NFormItem :label="t('sshd.port')">
                <NInputNumber v-model:value="form.port" :min="1" :max="65535" :disabled="!canManage" style="width: 160px" data-testid="sshd-port" />
                <span class="sd-muted small hint">{{ t('sshd.portHint') }}</span>
              </NFormItem>
              <NFormItem :label="t('sshd.passwordAuth')">
                <NSwitch v-model:value="form.passwordAuth" :disabled="!canManage" />
                <span class="sd-muted small hint">{{ t('sshd.passwordHint') }}</span>
              </NFormItem>
              <NFormItem :label="t('sshd.pubkeyAuth')">
                <NSwitch v-model:value="form.pubkeyAuth" :disabled="!canManage" />
              </NFormItem>
              <NFormItem :label="t('sshd.rootLogin')">
                <NSelect v-model:value="form.permitRootLogin" :options="rootOptions" :disabled="!canManage" style="width: 260px" />
              </NFormItem>
              <NFormItem v-if="canManage" label=" ">
                <NButton type="primary" :disabled="!!pending || loading" data-testid="sshd-save" @click="save">{{ t('sshd.save') }}</NButton>
                <span class="sd-muted small hint">{{ t('sshd.saveHint') }}</span>
              </NFormItem>
            </NForm>
          </template>
        </div>
      </NTabPane>
      <NTabPane name="keys" :tab="t('sshd.keys')">
        <div class="sd-toolbar">
          <span>{{ t('sshd.account') }}</span>
          <NSelect v-model:value="keyUser" size="small" :options="users.map((u) => ({ value: u.name, label: u.name }))" style="width: 160px" />
          <span class="sd-muted small sd-mono">{{ keyPath }}</span>
          <span style="flex: 1" />
          <NButton v-if="canManage" size="small" type="primary" @click="showKey = true">{{ t('sshd.addKey') }}</NButton>
        </div>
        <NDataTable class="sd-fill" size="small" flex-height :columns="keyCols" :data="keys" :row-key="(k: SshKey) => k.line" :bordered="false" />
      </NTabPane>
      <NTabPane name="logins" :tab="t('sshd.logins')">
        <div class="sd-toolbar">
          <template v-if="logins">
            <span>{{ t('sshd.loginStats', { ok: logins.success, fail: logins.failed }) }}</span>
            <span class="sd-muted small">{{ logins.source ? t('sshd.source', { src: logins.source }) : t('sshd.noLog') }}</span>
          </template>
          <span style="flex: 1" />
          <NSwitch v-model:value="onlyFailed" size="small" />
          <span class="small">{{ t('sshd.onlyFailed') }}</span>
          <NButton size="small" quaternary @click="loadLogins">{{ t('explorer.refresh') }}</NButton>
        </div>
        <div v-if="logins?.topFailed.length" class="top small">
          <b>{{ t('sshd.topFailed') }}</b>
          <span v-for="x in logins.topFailed.slice(0, 8)" :key="x.ip" class="sd-mono">{{ x.ip }} ×{{ x.failed }}</span>
          <span class="sd-muted">{{ t('sshd.topHint') }}</span>
        </div>
        <NDataTable class="sd-fill" size="small" flex-height virtual-scroll :columns="loginCols" :data="loginRows" :row-key="(l: SshLogin) => l.time + l.ip + l.port" :bordered="false" />
      </NTabPane>
    </NTabs>

    <NModal v-model:show="showKey" preset="card" :title="t('sshd.addKeyFor', { user: keyUser })" style="width: 560px; max-width: calc(100vw - 32px)">
      <NInput v-model:value="newKey" type="textarea" :autosize="{ minRows: 4, maxRows: 8 }" class="code" placeholder="ssh-ed25519 AAAA… user@pc" />
      <div class="sd-muted small" style="margin-top: 6px">{{ t('sshd.addKeyHint') }}</div>
      <template #footer>
        <div class="foot">
          <NButton @click="showKey = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :disabled="!newKey.trim()" @click="addKey">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.sshd {
  background: var(--sd-surface);
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
.body {
  padding: 10px 14px;
  display: flex;
  flex-direction: column;
  gap: 10px;
  overflow: auto;
}
.form {
  max-width: 720px;
}
.hint {
  margin-left: 10px;
}
.small,
:deep(.small) {
  font-size: 12px;
}
.top {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  padding: 0 12px 6px;
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
