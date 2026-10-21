<script setup lang="ts">
// 证书：申请（Let's Encrypt / ZeroSSL / 自定义 ACME，HTTP 或 DNS 验证）、上传、自动续期；
// DNS 服务商账号；服务器上 certbot / acme.sh 的证书可以迁移为面板续期。
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NDataTable, NDrawer, NDrawerContent, NDropdown, NForm, NFormItem, NInput, NModal, NRadioButton, NRadioGroup, NSelect,
  NSwitch, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api, request } from '@/api/http'
import type { CertSettings, Certificate, DNSAccount, ExternalCert, Task } from '@/api/types'
import { useNotify } from '@/stores/notify'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import TaskLog from '@/components/TaskLog.vue'
import { errorText, formatTime } from '@/i18n'

const props = defineProps<{ canManage: boolean }>()
const { t } = useI18n()
const message = useMessage()
const notify = useNotify()
const hostInfo = useHost()
const base = '/nodes/local/certs'

const certs = ref<Certificate[]>([])
const settings = ref<CertSettings | null>(null)
const accounts = ref<DNSAccount[]>([])
const loading = ref(false)
const logTask = ref<Task | null>(null)

async function load() {
  loading.value = true
  try {
    ;[certs.value, settings.value, accounts.value] = await Promise.all([
      api.get<Certificate[]>(base),
      api.get<CertSettings>(base + '/settings'),
      api.get<DNSAccount[]>(base + '/dns-accounts'),
    ])
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const statusType = (s: string) => (({ valid: 'success', failed: 'error', applying: 'info', pending: 'default', expired: 'warning' }) as Record<string, 'success' | 'error' | 'info' | 'default' | 'warning'>)[s] ?? 'default'

/* ---------- 申请 ---------- */
const showApply = ref(false)
const editing = ref<Certificate | null>(null)
const form = ref({ domains: '', ca: 'letsencrypt', caUrl: '', challenge: 'http', dnsAccountId: 0, keyType: 'ec256', autoRenew: true, remark: '' })
function openApply(c?: Certificate) {
  editing.value = c ?? null
  form.value = c
    ? { domains: c.domains, ca: c.ca || 'letsencrypt', caUrl: c.caUrl, challenge: c.challenge || 'http', dnsAccountId: c.dnsAccountId, keyType: c.keyType || 'ec256', autoRenew: c.autoRenew, remark: c.remark }
    : { domains: '', ca: 'letsencrypt', caUrl: '', challenge: 'http', dnsAccountId: accounts.value[0]?.id ?? 0, keyType: 'ec256', autoRenew: true, remark: '' }
  showApply.value = true
}
const caOptions = computed(() => [
  { value: 'letsencrypt', label: "Let's Encrypt" },
  { value: 'zerossl', label: 'ZeroSSL' },
  { value: 'letsencrypt-staging', label: t('certs.staging') },
  { value: 'custom', label: t('certs.customCa') },
])
const keyOptions = ['ec256', 'ec384', 'rsa2048', 'rsa4096'].map((k) => ({ value: k, label: k.toUpperCase().replace(/(\d+)/, ' $1') }))
const wildcard = computed(() => form.value.domains.includes('*'))
const saving = ref(false)
async function apply() {
  saving.value = true
  try {
    if (editing.value) {
      await api.put(`${base}/${editing.value.id}`, form.value)
      showApply.value = false
      message.success(t('common.saved'))
      load()
      return
    }
    const r = await api.post<{ cert: Certificate; task: Task }>(base, form.value)
    showApply.value = false
    logTask.value = r.task
    load()
    await notify.waitTask(r.task)
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

async function issue(c: Certificate) {
  try {
    const task = await api.post<Task>(`${base}/${c.id}/issue`)
    logTask.value = task
    load()
    await notify.waitTask(task)
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

/* ---------- 上传 ---------- */
const showUpload = ref(false)
const upload = ref({ id: 0, cert: '', key: '', remark: '' })
function openUpload(c?: Certificate) {
  upload.value = { id: c?.id ?? 0, cert: '', key: '', remark: c?.remark ?? '' }
  showUpload.value = true
}
async function doUpload() {
  try {
    await api.post(base + '/upload', upload.value)
    showUpload.value = false
    message.success(t('common.saved'))
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function remove(c: Certificate) {
  try {
    const ok = await withDanger(
      { level: 1, action: t('certs.delete'), node: hostInfo.label, impact: [c.domains, c.source === 'certbot' ? t('certs.deleteCertbotImpact') : t('certs.deleteImpact')], confirmText: t('common.delete') },
      () => api.del(`${base}/${c.id}`).then(() => true),
    )
    if (ok) load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function download(c: Certificate) {
  try {
    const ok = await withDanger(
      { level: 2, action: t('certs.download'), node: hostInfo.label, impact: t('certs.downloadImpact'), confirmText: t('certs.download') },
      (pw) => request<{ cert: string; key: string }>(`${base}/${c.id}/download`, { confirmPassword: pw }),
    )
    if (!ok) return
    for (const [name, text] of [['fullchain.pem', ok.cert], ['privkey.pem', ok.key]]) {
      const a = document.createElement('a')
      a.href = URL.createObjectURL(new Blob([text], { type: 'application/x-pem-file' }))
      a.download = `${c.domains.split(' ')[0].replace('*', '_')}-${name}`
      a.click()
      URL.revokeObjectURL(a.href)
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

/* ---------- 证书详情 ---------- */
const detail = ref<{ cert: Certificate; pem?: string } | null>(null)
async function showDetail(c: Certificate) {
  try {
    detail.value = await api.get<{ cert: Certificate; pem?: string }>(`${base}/${c.id}`)
  } catch (e) {
    message.error(errorText(e))
  }
}

function moreOptions(c: Certificate) {
  const o = [{ key: 'detail', label: t('certs.detail') }]
  if (props.canManage) {
    if (c.source === 'acme' || c.source === 'certbot') o.push({ key: 'edit', label: t('certs.editParams') })
    if (c.source === 'upload') o.push({ key: 'replace', label: t('certs.replace') })
    o.push({ key: 'download', label: t('certs.download') })
    o.push({ key: 'delete', label: t('common.delete') })
  }
  return o
}
function onMore(c: Certificate, k: string) {
  if (k === 'detail') showDetail(c)
  else if (k === 'edit') openApply(c)
  else if (k === 'replace') openUpload(c)
  else if (k === 'download') download(c)
  else if (k === 'delete') remove(c)
}

const columns = computed<DataTableColumns<Certificate>>(() => [
  {
    title: t('certs.domains'),
    key: 'domains',
    minWidth: 200,
    render: (c) =>
      h('div', { class: 'cell' }, [
        h('b', {}, c.domains.split(' ').join('，')),
        h('div', { class: 'sd-muted small' }, [t('certs.source.' + c.source), c.issuer ? ' · ' + c.issuer : '', c.remark ? ' · ' + c.remark : '']),
      ]),
  },
  {
    title: t('certs.status'),
    key: 'status',
    width: 150,
    render: (c) =>
      h('div', { class: 'cell' }, [
        h(NTag, { size: 'small', type: c.busy ? 'info' : statusType(c.status), bordered: false }, () => (c.busy ? t('certs.st.applying') : t('certs.st.' + c.status))),
        c.error ? h('span', { class: 'err small sd-ellipsis', title: c.error }, c.error) : null,
      ]),
  },
  {
    title: t('certs.expires'),
    key: 'notAfter',
    width: 170,
    render: (c) =>
      c.notAfter
        ? h('div', { class: 'cell' }, [
            h('span', { class: 'small' }, formatTime(c.notAfter)),
            h(NTag, { size: 'tiny', type: c.daysLeft < 14 ? 'error' : c.daysLeft < 30 ? 'warning' : 'success', bordered: false }, () => t('websites.daysLeft', { n: c.daysLeft })),
          ])
        : '-',
  },
  {
    title: t('certs.autoRenew'),
    key: 'autoRenew',
    width: 90,
    render: (c) => (c.source === 'acme' || c.source === 'certbot' ? (c.autoRenew ? t('certs.yes') : t('certs.no')) : h('span', { class: 'sd-muted' }, '-')),
  },
  {
    title: t('certs.usedBy'),
    key: 'sites',
    minWidth: 120,
    render: (c) => [...c.sites, ...(c.panel ? [t('certs.panel')] : [])].join('，') || h('span', { class: 'sd-muted' }, '-'),
  },
  {
    title: '',
    key: 'actions',
    width: 140,
    render: (c) =>
      h('div', { class: 'acts' }, [
        props.canManage && (c.source === 'acme' || c.source === 'certbot')
          ? h(NButton, { size: 'tiny', secondary: true, disabled: c.busy, onClick: () => issue(c), 'data-testid': 'cert-issue-' + c.id }, () => (c.status === 'valid' ? t('certs.renew') : t('certs.issue')))
          : null,
        h(NDropdown, { trigger: 'click', options: moreOptions(c), onSelect: (k: string) => onMore(c, k) }, () => h(NButton, { size: 'tiny', quaternary: true }, () => '…')),
      ]),
  },
])

/* ---------- ACME 邮箱 ---------- */
const showSettings = ref(false)
const email = ref('')
async function saveSettings() {
  try {
    await api.put(base + '/settings', { email: email.value })
    showSettings.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

/* ---------- DNS 账号 ---------- */
const showAccounts = ref(false)
const accForm = ref<{ id: number; name: string; provider: string; fields: Record<string, string> } | null>(null)
const providerOf = (key: string) => settings.value?.dnsProviders.find((p) => p.key === key)
function editAccount(a?: DNSAccount) {
  const provider = a?.provider ?? settings.value?.dnsProviders[0]?.key ?? 'cloudflare'
  accForm.value = { id: a?.id ?? 0, name: a?.name ?? '', provider, fields: {} }
}
async function saveAccount() {
  if (!accForm.value) return
  try {
    const { id, ...body } = accForm.value
    if (id) await api.put(`${base}/dns-accounts/${id}`, body)
    else await api.post(base + '/dns-accounts', body)
    accForm.value = null
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}
async function removeAccount(a: DNSAccount) {
  try {
    const ok = await withDanger({ level: 1, action: t('certs.deleteAccount'), impact: a.name, confirmText: t('common.delete') }, () => api.del(`${base}/dns-accounts/${a.id}`).then(() => true))
    if (ok) load()
  } catch (e) {
    message.error(errorText(e))
  }
}

/* ---------- 服务器上已有的证书 ---------- */
const showExternal = ref(false)
const externals = ref<ExternalCert[]>([])
async function openExternal() {
  showExternal.value = true
  try {
    externals.value = await api.get<ExternalCert[]>(base + '/external')
  } catch (e) {
    message.error(errorText(e))
  }
}
async function migrate(x: ExternalCert) {
  try {
    const ok = await withDanger(
      { level: 1, action: t('certs.migrate'), node: hostInfo.label, impact: [x.name, t('certs.migrateImpact')], confirmText: t('certs.migrate') },
      () => api.post<{ cert: Certificate; task?: Task }>(base + '/external/migrate', { name: x.name, challenge: 'http', ca: 'letsencrypt', renew: false }),
    )
    if (ok) {
      message.success(t('certs.migrated'))
      openExternal()
      load()
    }
  } catch (e) {
    message.error(errorText(e))
  }
}
</script>

<template>
  <div class="certs">
    <div class="sd-toolbar">
      <span class="sd-muted small">{{ settings?.email ? t('certs.emailIs', { email: settings.email }) : t('certs.noEmail') }}</span>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <template v-if="canManage">
        <NButton size="small" quaternary @click="openExternal">{{ t('certs.external') }}</NButton>
        <NButton size="small" quaternary @click="showAccounts = true">{{ t('certs.accounts') }}</NButton>
        <NButton size="small" quaternary @click="(email = settings?.email ?? ''), (showSettings = true)">{{ t('certs.settings') }}</NButton>
        <NButton size="small" @click="openUpload()">{{ t('certs.upload') }}</NButton>
        <NButton size="small" type="primary" data-testid="cert-apply" @click="openApply()">{{ t('certs.apply') }}</NButton>
      </template>
    </div>
    <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="columns" :data="certs" :row-key="(c: Certificate) => c.id" :bordered="false" :row-props="(c: Certificate) => ({ style: '', 'data-testid': 'cert-' + c.id })" />

    <NModal v-model:show="showApply" preset="card" :title="editing ? t('certs.editParams') : t('certs.apply')" style="width: 600px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="100" size="small">
        <NFormItem :label="t('certs.domains')" required>
          <NInput v-model:value="form.domains" type="textarea" :autosize="{ minRows: 1, maxRows: 4 }" :disabled="editing?.source === 'certbot'" :placeholder="t('certs.domainsPh')" data-testid="cert-domains" />
        </NFormItem>
        <NFormItem :label="t('certs.ca')">
          <NSelect v-model:value="form.ca" :options="caOptions" />
        </NFormItem>
        <NFormItem v-if="form.ca === 'custom'" :label="t('certs.caUrl')">
          <NInput v-model:value="form.caUrl" placeholder="https://acme.example.com/directory" />
        </NFormItem>
        <NFormItem :label="t('certs.challenge')">
          <NRadioGroup v-model:value="form.challenge" size="small">
            <NRadioButton value="http" :disabled="wildcard">{{ t('certs.http') }}</NRadioButton>
            <NRadioButton value="dns">{{ t('certs.dns') }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem v-if="form.challenge === 'dns'" :label="t('certs.dnsAccount')">
          <NSelect v-model:value="form.dnsAccountId" :options="accounts.map((a) => ({ value: a.id, label: `${a.name}（${providerOf(a.provider)?.name ?? a.provider}）` }))" style="flex: 1" />
          <NButton style="margin-left: 8px" @click="(showAccounts = true), editAccount()">{{ t('certs.addAccount') }}</NButton>
        </NFormItem>
        <div class="sd-muted small hint">{{ form.challenge === 'http' ? t('certs.httpHint') : t('certs.dnsHint') }}</div>
        <NFormItem :label="t('certs.keyType')">
          <NSelect v-model:value="form.keyType" :options="keyOptions" style="width: 160px" />
        </NFormItem>
        <NFormItem :label="t('certs.autoRenew')">
          <NSwitch v-model:value="form.autoRenew" />
        </NFormItem>
        <NFormItem :label="t('websites.remark')">
          <NInput v-model:value="form.remark" maxlength="128" />
        </NFormItem>
        <NAlert v-if="form.ca === 'zerossl' && !settings?.email" type="warning" :bordered="false">{{ t('certs.zerosslEmail') }}</NAlert>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="showApply = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="saving" :disabled="!form.domains.trim()" data-testid="cert-submit" @click="apply">{{ editing ? t('common.save') : t('certs.apply') }}</NButton>
        </div>
      </template>
    </NModal>

    <NModal v-model:show="showUpload" preset="card" :title="upload.id ? t('certs.replace') : t('certs.upload')" style="width: 640px; max-width: calc(100vw - 32px)">
      <NForm label-placement="top" size="small">
        <NFormItem :label="t('certs.certPem')">
          <NInput v-model:value="upload.cert" type="textarea" class="code" :autosize="{ minRows: 5, maxRows: 10 }" placeholder="-----BEGIN CERTIFICATE-----" />
        </NFormItem>
        <NFormItem :label="t('certs.keyPem')">
          <NInput v-model:value="upload.key" type="textarea" class="code" :autosize="{ minRows: 5, maxRows: 10 }" placeholder="-----BEGIN PRIVATE KEY-----" />
        </NFormItem>
        <NFormItem :label="t('websites.remark')">
          <NInput v-model:value="upload.remark" maxlength="128" />
        </NFormItem>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="showUpload = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :disabled="!upload.cert.trim() || !upload.key.trim()" @click="doUpload">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>

    <NModal v-model:show="showSettings" preset="card" :title="t('certs.settings')" style="width: 480px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="80" size="small">
        <NFormItem :label="t('certs.email')">
          <NInput v-model:value="email" placeholder="admin@example.com" />
        </NFormItem>
        <div class="sd-muted small">{{ t('certs.emailHint') }}</div>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="showSettings = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" @click="saveSettings">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>

    <NDrawer v-model:show="showAccounts" :width="560" placement="right">
      <NDrawerContent :title="t('certs.accounts')" closable>
        <div v-for="a in accounts" :key="a.id" class="acc">
          <b>{{ a.name }}</b>
          <span class="sd-muted small">{{ providerOf(a.provider)?.name ?? a.provider }} · {{ a.keys.join('、') }}</span>
          <span style="flex: 1" />
          <NButton size="tiny" quaternary @click="editAccount(a)">{{ t('common.edit') }}</NButton>
          <NButton size="tiny" quaternary type="error" @click="removeAccount(a)">{{ t('common.delete') }}</NButton>
        </div>
        <div v-if="!accounts.length" class="sd-muted small">{{ t('certs.noAccounts') }}</div>
        <NButton v-if="!accForm" size="small" style="margin-top: 10px" @click="editAccount()">{{ t('certs.addAccount') }}</NButton>
        <NForm v-if="accForm" label-placement="left" label-width="120" size="small" class="accform">
          <NFormItem :label="t('certs.accountName')">
            <NInput v-model:value="accForm.name" maxlength="64" />
          </NFormItem>
          <NFormItem :label="t('certs.provider')">
            <NSelect v-model:value="accForm.provider" :options="(settings?.dnsProviders ?? []).map((p) => ({ value: p.key, label: p.name }))" :disabled="!!accForm.id" />
          </NFormItem>
          <NFormItem v-for="f in providerOf(accForm.provider)?.fields ?? []" :key="f" :label="f">
            <NInput v-model:value="accForm.fields[f]" type="password" show-password-on="click" :placeholder="accForm.id ? t('certs.keepSecret') : ''" />
          </NFormItem>
          <div class="foot">
            <NButton size="small" @click="accForm = null">{{ t('common.cancel') }}</NButton>
            <NButton size="small" type="primary" :disabled="!accForm.name.trim()" @click="saveAccount">{{ t('common.save') }}</NButton>
          </div>
        </NForm>
      </NDrawerContent>
    </NDrawer>

    <NDrawer v-model:show="showExternal" :width="640" placement="right">
      <NDrawerContent :title="t('certs.external')" closable>
        <div v-if="!externals.length" class="sd-muted small">{{ t('certs.noExternal') }}</div>
        <div v-for="x in externals" :key="x.tool + x.name" class="ext">
          <div class="exthead">
            <NTag size="small" :bordered="false">{{ x.tool }}</NTag>
            <b>{{ x.info?.domains.join('，') ?? x.name }}</b>
            <span style="flex: 1" />
            <NTag v-if="x.migrated" size="small" type="success" :bordered="false">{{ t('certs.migrated') }}</NTag>
            <NButton v-else-if="canManage && x.tool === 'certbot' && !x.error" size="tiny" type="primary" @click="migrate(x)">{{ t('certs.migrate') }}</NButton>
          </div>
          <div class="sd-muted small sd-mono">{{ x.cert }}</div>
          <div v-if="x.info" class="small">{{ x.info.issuer }} · {{ formatTime(x.info.notAfter) }}</div>
          <div v-if="x.error" class="err small">{{ x.error }}</div>
          <ul class="small sd-muted">
            <li v-for="n in x.notes" :key="n">{{ n }}</li>
          </ul>
        </div>
      </NDrawerContent>
    </NDrawer>

    <NDrawer :show="!!detail" :width="640" placement="right" @update:show="(v: boolean) => !v && (detail = null)">
      <NDrawerContent :title="detail?.cert.domains" closable>
        <div v-if="detail" class="small">
          <p>{{ t('certs.files') }}：<span class="sd-mono">{{ detail.cert.certPath }}</span>、<span class="sd-mono">{{ detail.cert.keyPath }}</span></p>
          <p v-if="detail.cert.error" class="err">{{ detail.cert.error }}</p>
          <pre class="pem sd-mono">{{ detail.pem }}</pre>
        </div>
      </NDrawerContent>
    </NDrawer>
    <TaskLog v-model:task="logTask" />
  </div>
</template>

<style scoped>
.certs {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.small,
:deep(.small) {
  font-size: 12px;
}
:deep(.cell) {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
  align-items: flex-start;
}
:deep(.acts) {
  display: flex;
  gap: 4px;
}
:deep(.err),
.err {
  color: var(--sd-danger);
  max-width: 100%;
}
.hint {
  margin: -6px 0 10px 100px;
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
.acc {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 6px 0;
  border-bottom: 1px solid var(--sd-border);
}
.accform {
  margin-top: 12px;
}
.ext {
  border: 1px solid var(--sd-border);
  border-radius: 6px;
  padding: 8px 10px;
  margin-bottom: 8px;
}
.exthead {
  display: flex;
  gap: 8px;
  align-items: center;
}
.ext ul {
  margin: 4px 0 0;
  padding-left: 18px;
}
.pem {
  font-size: 11px;
  background: var(--sd-bg);
  padding: 8px;
  border-radius: 4px;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>
