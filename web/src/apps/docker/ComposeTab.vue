<script setup lang="ts">
// Compose 项目：新建（粘贴 YAML 或登记已有目录）、启动 / 停止 / 重启 / 更新、编辑配置、日志、删除。
import { computed, h, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NButton, NButtonGroup, NCheckbox, NDataTable, NDropdown, NForm, NFormItem, NInput, NModal, NRadioButton, NRadioGroup,
  NTabPane, NTabs, NTag, useMessage, type DataTableColumns,
} from 'naive-ui'
import { api } from '@/api/http'
import type { ComposeProject, Task } from '@/api/types'
import { useSession } from '@/stores/session'
import { useNotify } from '@/stores/notify'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import LogStream from '@/components/LogStream.vue'
import TaskLog from '@/components/TaskLog.vue'
import { errorText } from '@/i18n'
import { portText, stateType } from './util'

const props = defineProps<{ active: boolean; compose: boolean }>()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const notify = useNotify()
const hostInfo = useHost()
const canManage = computed(() => session.has('docker:manage'))
const base = '/nodes/local/docker/compose'

const list = ref<ComposeProject[]>([])
const loading = ref(false)
const selected = ref<string | null>(null)
const logTask = ref<Task | null>(null)
let timer: number | undefined

async function load() {
  loading.value = true
  try {
    list.value = await api.get<ComposeProject[]>(base)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(() => {
  load()
  timer = window.setInterval(() => props.active && load(), 8000)
})
onUnmounted(() => clearInterval(timer))
watch(() => props.active, (v) => v && load())

const cur = computed(() => list.value.find((p) => p.name === selected.value) ?? null)

async function track(task: Task, show = true) {
  if (show) logTask.value = task
  const done = await notify.waitTask(task)
  load()
  return done
}

async function action(p: ComposeProject, a: string) {
  try {
    const run = () => api.post<Task>(`${base}/${p.name}/${a}`)
    const task = ['stop', 'restart', 'down'].includes(a)
      ? await withDanger(
          {
            level: 1,
            action: t('docker.composeAction', { action: t('docker.cact.' + a) }),
            node: hostInfo.label,
            impact: [p.name, a === 'down' ? t('docker.downImpact') : t('docker.composeImpact', { n: p.total })],
            confirmText: `${t('docker.cact.' + a)} ${p.name}`,
          },
          run,
        )
      : await run()
    if (task) await track(task, a === 'up' || a === 'pull')
  } catch (e) {
    message.error(errorText(e))
  }
}

const delVolumes = ref(false)
const delFiles = ref(false)
async function remove(p: ComposeProject) {
  delVolumes.value = false
  delFiles.value = false
  showDelete.value = p
}
const showDelete = ref<ComposeProject | null>(null)
async function confirmDelete() {
  const p = showDelete.value
  if (!p) return
  showDelete.value = null
  const level = delVolumes.value || delFiles.value ? 2 : 1
  const impact = [t('docker.deleteProjectImpact', { n: p.total })]
  if (delVolumes.value) impact.push(t('docker.deleteVolumesImpact'))
  if (delFiles.value) impact.push(t('docker.deleteFilesImpact', { dir: p.dir }))
  try {
    const task = await withDanger(
      { level, action: t('docker.deleteProject'), node: hostInfo.label, impact, confirmText: t('docker.deleteNamed', { name: p.name }) },
      (pw) =>
        api.del<Task>(`${base}/${p.name}`, {
          query: { volumes: delVolumes.value ? '1' : undefined, files: delFiles.value ? '1' : undefined },
          confirmPassword: pw,
        }),
    )
    if (task) {
      if (selected.value === p.name) selected.value = null
      await track(task, false)
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

function moreOptions(p: ComposeProject) {
  return [
    { label: t('docker.logs'), key: 'logs' },
    ...(canManage.value
      ? [
          { label: t('docker.editConfig'), key: 'edit', disabled: p.status === 'missing' },
          { label: t('docker.cact.pull'), key: 'pull', disabled: p.status === 'missing' },
          { label: t('docker.cact.down'), key: 'down', disabled: p.status === 'missing' || !p.total },
          { type: 'divider', key: 'd' },
          { label: t('common.delete'), key: 'delete', disabled: !!p.app },
        ]
      : []),
  ]
}

function onMore(p: ComposeProject, key: string) {
  if (key === 'logs') {
    selected.value = p.name
    sideTab.value = 'logs'
  } else if (key === 'edit') openEdit(p)
  else if (key === 'delete') remove(p)
  else action(p, key)
}

const columns = computed<DataTableColumns<ComposeProject>>(() => [
  {
    title: t('docker.project'),
    key: 'name',
    minWidth: 160,
    sorter: (a, b) => a.name.localeCompare(b.name),
    render: (p) =>
      h('div', { class: 'cname' }, [
        h('span', { class: 'sd-mono' }, p.name),
        p.app ? h(NTag, { size: 'tiny', type: 'info', bordered: false }, () => t('docker.fromApp')) : null,
        p.managed ? h(NTag, { size: 'tiny', bordered: false }, () => t('docker.managed')) : null,
      ]),
  },
  {
    title: t('docker.state'),
    key: 'status',
    width: 140,
    render: (p) => h(NTag, { size: 'small', type: stateType(p.status), bordered: false }, () => `${t('docker.cst.' + p.status)} ${p.running}/${p.total}`),
  },
  { title: t('docker.services'), key: 'services', minWidth: 120, ellipsis: { tooltip: true }, render: (p) => p.services.join(', ') || '-' },
  { title: t('docker.dir'), key: 'dir', minWidth: 200, ellipsis: { tooltip: true }, render: (p) => h('span', { class: 'sd-mono small' }, p.dir) },
  {
    title: '',
    key: 'actions',
    width: 160,
    render: (p) =>
      h('div', { class: 'acts', onClick: (e: MouseEvent) => e.stopPropagation() }, [
        canManage.value && p.status !== 'missing'
          ? h(NButtonGroup, { size: 'tiny' }, () =>
              (p.status === 'stopped' ? ['up'] : ['restart', 'stop']).map((a) =>
                h(NButton, { size: 'tiny', secondary: true, type: a === 'stop' ? 'error' : 'default', onClick: () => action(p, a), 'data-testid': `cp-${a}-${p.name}` }, () => t('docker.cact.' + a)),
              ),
            )
          : null,
        h(NDropdown, { trigger: 'click', options: moreOptions(p), onSelect: (k: string) => onMore(p, k) }, () => h(NButton, { size: 'tiny', quaternary: true, 'data-testid': 'cp-more-' + p.name }, () => '⋯')),
      ]),
  },
])

const rowProps = (p: ComposeProject) => ({
  class: p.name === selected.value ? 'row-sel' : '',
  'data-testid': 'cp-' + p.name,
  onClick: () => (selected.value = p.name),
})
const sideTab = ref<'info' | 'logs'>('info')

/* ---------- 新建 ---------- */
const sample = `services:
  web:
    image: nginx:alpine
    restart: unless-stopped
    ports:
      - "8080:80"
    volumes:
      - ./html:/usr/share/nginx/html
`
const showCreate = ref(false)
const cform = ref({ name: '', mode: 'yaml' as 'yaml' | 'dir', content: sample, env: '', dir: '', start: true })
const saving = ref(false)

function openCreate() {
  cform.value = { name: '', mode: 'yaml', content: sample, env: '', dir: '', start: true }
  showCreate.value = true
}

async function create() {
  saving.value = true
  try {
    const r = await api.post<{ task?: Task }>(base, cform.value)
    showCreate.value = false
    if (r.task) await track(r.task)
    else load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

/* ---------- 编辑 ---------- */
const editing = ref<ComposeProject | null>(null)
const eform = ref({ content: '', env: '', redeploy: true })
async function openEdit(p: ComposeProject) {
  try {
    const r = await api.get<{ content: string; env: string }>(`${base}/${p.name}`)
    eform.value = { content: r.content, env: r.env, redeploy: true }
    editing.value = p
  } catch (e) {
    message.error(errorText(e))
  }
}

async function saveEdit() {
  if (!editing.value) return
  saving.value = true
  try {
    const r = await api.put<{ task?: Task }>(`${base}/${editing.value.name}`, eform.value)
    editing.value = null
    message.success(t('common.saved'))
    if (r?.task) await track(r.task)
    else load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="cp">
    <NAlert v-if="!props.compose" type="warning" :bordered="false" class="warn">{{ t('docker.noComposeHint') }}</NAlert>
    <div class="sd-toolbar">
      <b>{{ t('docker.projectCount', { n: list.length }) }}</b>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <NButton v-if="canManage" size="small" type="primary" :disabled="!props.compose" data-testid="cp-create" @click="openCreate">{{ t('docker.createProject') }}</NButton>
    </div>
    <div class="main">
      <NDataTable
        class="sd-fill"
        size="small"
        flex-height
        :loading="loading && !list.length"
        :columns="columns"
        :data="list"
        :row-key="(p: ComposeProject) => p.name"
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
              <NAlert v-if="cur.status === 'missing'" type="warning" :bordered="false">{{ t('docker.missingHint') }}</NAlert>
              <dl class="kv">
                <dt>{{ t('docker.dir') }}</dt>
                <dd class="sd-mono">{{ cur.dir }}</dd>
                <dt>{{ t('docker.files') }}</dt>
                <dd class="sd-mono">{{ cur.files.join('，') || '-' }}</dd>
                <template v-if="cur.app">
                  <dt>{{ t('docker.fromApp') }}</dt>
                  <dd>{{ cur.app }}</dd>
                </template>
              </dl>
              <h4>{{ t('docker.containers') }}</h4>
              <div v-for="c in cur.containers" :key="c.id" class="crow">
                <NTag size="tiny" :type="stateType(c.state)" :bordered="false">{{ t('docker.st.' + c.state, c.state) }}</NTag>
                <span class="sd-mono">{{ c.service || c.name }}</span>
                <span class="sd-muted sd-ellipsis">{{ c.image }}</span>
                <span class="sd-mono small">{{ c.ports.filter((p) => p.public).map(portText).join(', ') }}</span>
              </div>
              <div v-if="!cur.containers.length" class="sd-muted">{{ t('docker.noContainers') }}</div>
            </div>
          </NTabPane>
          <NTabPane name="logs" :tab="t('docker.logs')">
            <LogStream :path="`${base}/${cur.name}/logs?tail=300`" :active="sideTab === 'logs' && props.active" />
          </NTabPane>
        </NTabs>
      </div>
    </div>

    <NModal v-model:show="showCreate" preset="card" :title="t('docker.createProject')" style="width: 720px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="96" size="small">
        <NFormItem :label="t('docker.projectName')" required>
          <NInput v-model:value="cform.name" placeholder="my-app" data-testid="cp-name" />
        </NFormItem>
        <NFormItem :label="t('docker.source')">
          <NRadioGroup v-model:value="cform.mode" size="small">
            <NRadioButton value="yaml">{{ t('docker.modeYaml') }}</NRadioButton>
            <NRadioButton value="dir">{{ t('docker.modeDir') }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <template v-if="cform.mode === 'yaml'">
          <NFormItem label="docker-compose.yml">
            <NInput v-model:value="cform.content" type="textarea" class="code sd-mono" :autosize="{ minRows: 10, maxRows: 18 }" data-testid="cp-yaml" />
          </NFormItem>
          <NFormItem label=".env">
            <NInput v-model:value="cform.env" type="textarea" class="code sd-mono" :autosize="{ minRows: 2, maxRows: 6 }" placeholder="KEY=VALUE" />
          </NFormItem>
          <NFormItem label=" ">
            <span class="sd-muted small">{{ t('docker.yamlSaveHint') }}</span>
          </NFormItem>
        </template>
        <NFormItem v-else :label="t('docker.dir')">
          <NInput v-model:value="cform.dir" placeholder="/opt/my-app" />
        </NFormItem>
        <NFormItem label=" ">
          <NCheckbox v-model:checked="cform.start">{{ t('docker.startNow') }}</NCheckbox>
        </NFormItem>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="showCreate = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="saving" :disabled="!cform.name.trim()" data-testid="cp-create-ok" @click="create">{{ t('common.ok') }}</NButton>
        </div>
      </template>
    </NModal>

    <NModal :show="!!editing" preset="card" :title="t('docker.editConfig') + ' · ' + (editing?.name ?? '')" style="width: 760px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && (editing = null)">
      <NForm label-placement="top" size="small">
        <NFormItem :label="editing?.files[0] ?? 'docker-compose.yml'">
          <NInput v-model:value="eform.content" type="textarea" class="code sd-mono" :autosize="{ minRows: 12, maxRows: 22 }" />
        </NFormItem>
        <NFormItem label=".env">
          <NInput v-model:value="eform.env" type="textarea" class="code sd-mono" :autosize="{ minRows: 2, maxRows: 6 }" />
        </NFormItem>
        <NCheckbox v-model:checked="eform.redeploy">{{ t('docker.redeploy') }}</NCheckbox>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="editing = null">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :loading="saving" @click="saveEdit">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>

    <NModal :show="!!showDelete" preset="card" :title="t('docker.deleteProject') + ' · ' + (showDelete?.name ?? '')" style="width: 460px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && (showDelete = null)">
      <div class="dopts">
        <span>{{ t('docker.deleteProjectHint') }}</span>
        <NCheckbox v-model:checked="delVolumes">{{ t('docker.deleteVolumes') }}</NCheckbox>
        <NCheckbox v-model:checked="delFiles" :disabled="!showDelete?.managed">{{ t('docker.deleteFiles') }}</NCheckbox>
      </div>
      <template #footer>
        <div class="foot">
          <NButton @click="showDelete = null">{{ t('common.cancel') }}</NButton>
          <NButton type="error" data-testid="cp-delete-ok" @click="confirmDelete">{{ t('common.delete') }}</NButton>
        </div>
      </template>
    </NModal>
    <TaskLog v-model:task="logTask" />
  </div>
</template>

<style scoped>
.cp {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.warn {
  margin: 8px 10px 0;
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
:deep(.acts) {
  display: flex;
  gap: 6px;
  align-items: center;
}
:deep(.small),
.small {
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
  margin: 8px 0;
}
.kv dt {
  color: var(--sd-text-2);
}
.kv dd {
  margin: 0;
  word-break: break-all;
}
h4 {
  margin: 12px 0 6px;
  font-size: 13px;
}
.crow {
  display: flex;
  gap: 8px;
  align-items: center;
  padding: 3px 0;
  min-width: 0;
}
.code :deep(textarea) {
  font-family: var(--sd-mono);
  font-size: 12px;
}
.dopts {
  display: flex;
  flex-direction: column;
  gap: 8px;
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
