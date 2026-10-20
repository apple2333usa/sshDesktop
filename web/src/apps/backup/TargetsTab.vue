<script setup lang="ts">
// 备份目标：本机目录、远程服务器（SFTP）、S3 兼容存储、WebDAV。可以测试连接，浏览目标中的备份文件并从文件恢复。
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NDataTable, NDrawer, NDrawerContent, NForm, NFormItem, NInput, NModal, NRadioButton, NRadioGroup, NSelect, NSwitch, NTag,
  useMessage, type DataTableColumns,
} from 'naive-ui'
import { api } from '@/api/http'
import type { BackupObject, BackupTarget, BackupTargetConfig, BackupTargetType } from '@/api/types'
import { useSession } from '@/stores/session'
import { useHost } from '@/stores/host'
import { useNodes } from '@/stores/nodes'
import { withDanger } from '@/components/danger'
import { errorText, formatBytes, formatTime } from '@/i18n'
import type { RestoreSource } from './types'

const props = defineProps<{ defaultLocal: string }>()
const emit = defineEmits<{ restore: [RestoreSource]; changed: [] }>()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const hostInfo = useHost()
const nodes = useNodes()
const canManage = computed(() => session.has('backup:manage'))
const base = '/nodes/local/backup/targets'

const list = ref<BackupTarget[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    list.value = await api.get<BackupTarget[]>(base)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
defineExpose({ load })
onMounted(load)

function where(x: BackupTarget) {
  const c = x.config
  switch (x.type) {
    case 'local':
      return c.path ?? ''
    case 'sftp':
      return `${nodes.map[c.nodeId ?? '']?.name ?? c.nodeId}:${c.path}`
    case 's3':
      return `${c.endpoint} / ${c.bucket}${c.path ? '/' + c.path : ''}`
    case 'webdav':
      return `${c.url}${c.path ? ' / ' + c.path : ''}`
  }
  return ''
}

async function test(x: BackupTarget) {
  try {
    const r = await api.post<{ files: number }>(base + '/test', { id: x.id, name: x.name, type: x.type, config: x.config })
    message.success(t('backup.testOk', { n: r.files }))
  } catch (e) {
    message.error(errorText(e))
  }
}

async function remove(x: BackupTarget) {
  try {
    const r = await withDanger(
      { level: 1, action: t('backup.deleteTarget'), node: hostInfo.label, impact: [x.name, where(x), t('backup.deleteTargetImpact')], confirmText: t('common.delete') },
      () => api.del(`${base}/${x.id}`).then(() => true),
    )
    if (r) {
      load()
      emit('changed')
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

const columns = computed<DataTableColumns<BackupTarget>>(() => [
  { title: t('backup.name'), key: 'name', minWidth: 140, render: (x) => h('b', {}, x.name) },
  { title: t('backup.targetType'), key: 'type', width: 110, render: (x) => h(NTag, { size: 'small', bordered: false }, () => t('backup.ttype.' + x.type)) },
  { title: t('backup.location'), key: 'loc', minWidth: 220, render: (x) => h('span', { class: 'sd-mono small sd-ellipsis' }, where(x)) },
  { title: t('backup.usedBy'), key: 'plans', width: 90, render: (x) => (x.plans ? t('backup.planCount', { n: x.plans }) : h('span', { class: 'sd-muted' }, '-')) },
  {
    title: '',
    key: 'actions',
    width: 230,
    render: (x) =>
      h('div', { class: 'acts' }, [
        canManage.value ? h(NButton, { size: 'tiny', secondary: true, onClick: () => test(x), 'data-testid': 'target-test-' + x.id }, () => t('backup.test')) : null,
        h(NButton, { size: 'tiny', quaternary: true, onClick: () => openFiles(x), 'data-testid': 'target-files-' + x.id }, () => t('backup.files')),
        canManage.value ? h(NButton, { size: 'tiny', quaternary: true, onClick: () => openForm(x) }, () => t('common.edit')) : null,
        canManage.value ? h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: () => remove(x) }, () => t('common.delete')) : null,
      ]),
  },
])

/* ---------- 新建与修改 ---------- */
interface Form {
  name: string
  type: BackupTargetType
  config: BackupTargetConfig
  secretKey: string
  password: string
}
const showForm = ref(false)
const editing = ref<BackupTarget | null>(null)
const form = ref<Form>({ name: '', type: 'local', config: {}, secretKey: '', password: '' })
const saving = ref(false)
const testing = ref(false)

function openForm(x?: BackupTarget) {
  editing.value = x ?? null
  form.value = x
    ? { name: x.name, type: x.type, config: { ...x.config }, secretKey: '', password: '' }
    : { name: '', type: 'local', config: { path: props.defaultLocal }, secretKey: '', password: '' }
  showForm.value = true
}

function body() {
  const f = form.value
  return { id: editing.value?.id ?? 0, name: f.name, type: f.type, config: f.config, secret: { secretKey: f.secretKey, password: f.password } }
}

async function testForm() {
  testing.value = true
  try {
    const r = await api.post<{ files: number }>(base + '/test', body())
    message.success(t('backup.testOk', { n: r.files }))
  } catch (e) {
    message.error(errorText(e))
  } finally {
    testing.value = false
  }
}

async function save() {
  saving.value = true
  try {
    if (editing.value) await api.put(`${base}/${editing.value.id}`, body())
    else await api.post(base, body())
    showForm.value = false
    message.success(t('common.saved'))
    load()
    emit('changed')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

const savedHint = computed(() => (editing.value?.hasSecret ? t('backup.secretSaved') : ''))
const remoteNodes = computed(() => nodes.remote.map((n) => ({ label: nodes.label(n.id), value: n.id })))

/* ---------- 目标中的文件 ---------- */
const filesOf = ref<BackupTarget | null>(null)
const files = ref<BackupObject[]>([])
const filesLoading = ref(false)

async function openFiles(x: BackupTarget) {
  filesOf.value = x
  files.value = []
  await loadFiles()
}
async function loadFiles() {
  const x = filesOf.value
  if (!x) return
  filesLoading.value = true
  try {
    files.value = await api.get<BackupObject[]>(`${base}/${x.id}/files`)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    filesLoading.value = false
  }
}

async function removeFile(o: BackupObject) {
  const x = filesOf.value
  if (!x) return
  try {
    const r = await withDanger(
      { level: 2, action: t('backup.deleteFile'), node: hostInfo.label, impact: [x.name, o.key], confirmText: t('common.delete') },
      (pw) => api.del(`${base}/${x.id}/files`, { query: { key: o.key }, confirmPassword: pw }).then(() => true),
    )
    if (r) {
      loadFiles()
      emit('changed')
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

// 从文件名推断备份类型：plan-<id>/<类型>_<来源>_<时间>.tar.gz[.enc]
function guessType(key: string): RestoreSource['type'] {
  const m = /^(dir|app|panel)_/.exec(key.split('/').pop() ?? '')
  return m ? (m[1] as RestoreSource['type']) : undefined
}

const fileCols = computed<DataTableColumns<BackupObject>>(() => [
  {
    title: t('backup.file'),
    key: 'key',
    minWidth: 260,
    render: (o) =>
      h('div', { class: 'fkey' }, [
        h('span', { class: 'sd-mono small sd-ellipsis', title: o.key }, o.key),
        o.encrypted ? h(NTag, { size: 'tiny', bordered: false, type: 'warning' }, () => t('backup.encrypted')) : null,
        o.recordId ? null : h(NTag, { size: 'tiny', bordered: false }, () => t('backup.noRecord')),
      ]),
  },
  { title: t('backup.size'), key: 'size', width: 90, render: (o) => formatBytes(o.size) },
  { title: t('backup.modified'), key: 'modified', width: 150, render: (o) => formatTime(o.modified) },
  {
    title: '',
    key: 'actions',
    width: 130,
    render: (o) =>
      canManage.value
        ? h('div', { class: 'acts' }, [
            h(
              NButton,
              {
                size: 'tiny',
                secondary: true,
                onClick: () => emit('restore', o.recordId ? { recordId: o.recordId, key: o.key, encrypted: o.encrypted, type: guessType(o.key) } : { targetId: filesOf.value!.id, key: o.key, encrypted: o.encrypted, type: guessType(o.key) }),
              },
              () => t('backup.restore'),
            ),
            h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: () => removeFile(o) }, () => t('common.delete')),
          ])
        : null,
  },
])
</script>

<template>
  <div class="targets">
    <div class="sd-toolbar">
      <span class="sd-muted small">{{ t('backup.targetsHint') }}</span>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <NButton v-if="canManage" size="small" type="primary" data-testid="target-add" @click="openForm()">{{ t('backup.addTarget') }}</NButton>
    </div>
    <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="columns" :data="list" :row-key="(x: BackupTarget) => x.id" :bordered="false" :row-props="(x: BackupTarget) => ({ style: '', 'data-testid': 'target-' + x.id })" />

    <NModal v-model:show="showForm" preset="card" :title="editing ? t('backup.editTarget') : t('backup.addTarget')" style="width: 580px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="104" size="small">
        <NFormItem :label="t('backup.targetType')">
          <NRadioGroup v-model:value="form.type" size="small" :disabled="!!editing">
            <NRadioButton v-for="x in ['local', 'sftp', 's3', 'webdav']" :key="x" :value="x" :disabled="x === 'sftp' && !remoteNodes.length">{{ t('backup.ttype.' + x) }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <NFormItem :label="t('backup.name')" required>
          <NInput v-model:value="form.name" maxlength="64" data-testid="target-name" />
        </NFormItem>
        <template v-if="form.type === 'local'">
          <NFormItem :label="t('backup.dir')" required>
            <NInput v-model:value="form.config.path" class="sd-mono" data-testid="target-path" />
          </NFormItem>
        </template>
        <template v-else-if="form.type === 'sftp'">
          <NFormItem :label="t('backup.server')" required>
            <NSelect v-model:value="form.config.nodeId" :options="remoteNodes" />
          </NFormItem>
          <NFormItem :label="t('backup.remoteDir')" required>
            <NInput v-model:value="form.config.path" class="sd-mono" placeholder="/root/sshdesktop-backups" />
          </NFormItem>
          <div class="sd-muted small hint">{{ t('backup.sftpHint') }}</div>
        </template>
        <template v-else-if="form.type === 's3'">
          <NFormItem :label="t('backup.endpoint')" required>
            <NInput v-model:value="form.config.endpoint" placeholder="https://s3.amazonaws.com" />
          </NFormItem>
          <NFormItem :label="t('backup.region')">
            <NInput v-model:value="form.config.region" placeholder="us-east-1" style="width: 200px" />
          </NFormItem>
          <NFormItem :label="t('backup.bucket')" required>
            <NInput v-model:value="form.config.bucket" />
          </NFormItem>
          <NFormItem :label="t('backup.prefix')">
            <NInput v-model:value="form.config.path" class="sd-mono" placeholder="sshdesktop" />
          </NFormItem>
          <NFormItem label="Access Key" required>
            <NInput v-model:value="form.config.accessKey" class="sd-mono" :input-props="{ autocomplete: 'off' }" />
          </NFormItem>
          <NFormItem label="Secret Key" :required="!editing?.hasSecret">
            <NInput v-model:value="form.secretKey" type="password" show-password-on="click" :placeholder="savedHint" :input-props="{ autocomplete: 'new-password' }" />
          </NFormItem>
          <NFormItem :label="t('backup.pathStyle')">
            <NSwitch v-model:value="form.config.pathStyle" />
            <span class="sd-muted small" style="margin-left: 8px">{{ t('backup.pathStyleHint') }}</span>
          </NFormItem>
        </template>
        <template v-else>
          <NFormItem :label="t('backup.webdavUrl')" required>
            <NInput v-model:value="form.config.url" placeholder="https://dav.example.com/remote.php/dav/files/me" />
          </NFormItem>
          <NFormItem :label="t('backup.remoteDir')">
            <NInput v-model:value="form.config.path" class="sd-mono" placeholder="sshdesktop" />
          </NFormItem>
          <NFormItem :label="t('backup.username')">
            <NInput v-model:value="form.config.username" :input-props="{ autocomplete: 'off' }" />
          </NFormItem>
          <NFormItem :label="t('backup.password')">
            <NInput v-model:value="form.password" type="password" show-password-on="click" :placeholder="savedHint" :input-props="{ autocomplete: 'new-password' }" />
          </NFormItem>
        </template>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton :loading="testing" data-testid="target-form-test" @click="testForm">{{ t('backup.test') }}</NButton>
          <span style="flex: 1" />
          <NButton @click="showForm = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="saving" :disabled="!form.name.trim()" data-testid="target-save" @click="save">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>

    <NDrawer :show="!!filesOf" :width="760" placement="right" @update:show="(v: boolean) => !v && (filesOf = null)">
      <NDrawerContent :title="t('backup.filesOf', { name: filesOf?.name ?? '' })" closable>
        <div class="files">
          <div class="sd-toolbar">
            <span class="sd-muted small">{{ t('backup.filesHint') }}</span>
            <span style="flex: 1" />
            <NButton size="small" quaternary @click="loadFiles">{{ t('explorer.refresh') }}</NButton>
          </div>
          <NAlert v-if="!filesLoading && !files.length" type="info" :bordered="false">{{ t('backup.noFiles') }}</NAlert>
          <NDataTable v-else class="sd-fill" size="small" flex-height :loading="filesLoading" :columns="fileCols" :data="files" :row-key="(o: BackupObject) => o.key" :bordered="false" />
        </div>
      </NDrawerContent>
    </NDrawer>
  </div>
</template>

<style scoped>
.targets {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.small,
:deep(.small) {
  font-size: 12px;
}
:deep(.acts),
:deep(.fkey) {
  display: flex;
  gap: 6px;
  align-items: center;
  min-width: 0;
}
.hint {
  margin: -6px 0 12px 104px;
}
.foot {
  display: flex;
  gap: 8px;
  align-items: center;
}
.files {
  height: 100%;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
</style>
