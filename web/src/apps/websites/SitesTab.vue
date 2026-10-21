<script setup lang="ts">
// 站点列表：反向代理和静态网站，启用 / 停用、日志、删除；接管来的站点可以还原为原配置。
import { computed, h, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDataTable, NDrawer, NDrawerContent, NDropdown, NRadioButton, NRadioGroup, NSwitch, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { api } from '@/api/http'
import type { WebRuntimeStatus, Website } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import { errorText } from '@/i18n'
import SiteForm from './SiteForm.vue'

const props = defineProps<{ runtime: WebRuntimeStatus; canManage: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const hostInfo = useHost()
const base = '/nodes/local/websites'

const sites = ref<Website[]>([])
const loading = ref(false)
async function load() {
  loading.value = true
  try {
    sites.value = await api.get<Website[]>(base + '/sites')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

/* ---------- 新建 / 修改 ---------- */
const formSite = ref<Partial<Website> | null>(null)
function openForm(w?: Website) {
  formSite.value = w ? { ...w } : {}
}
// 从应用商店打开：预填反向代理
watch(
  () => host.props.create as { appName: string; port: string; title?: string } | undefined,
  (c) => {
    if (!c) return
    formSite.value = { type: 'proxy', appName: c.appName, name: c.appName, proxyPass: `http://127.0.0.1:${c.port}`, webSocket: true, remark: c.title ?? '' }
    host.setState({ create: undefined })
  },
  { immediate: true },
)
function saved() {
  formSite.value = null
  load()
  emit('changed')
}

function url(w: Website) {
  const d = w.domains.split(' ')[0].replace(/^\*\./, 'www.')
  if (w.https) return `https://${d}${w.httpsPort === 443 ? '' : ':' + w.httpsPort}/`
  return `http://${d}${w.httpPort === 80 ? '' : ':' + w.httpPort}/`
}

async function toggle(w: Website, enabled: boolean) {
  try {
    await api.post(`${base}/sites/${w.id}/enable`, { enabled })
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function remove(w: Website, files: boolean) {
  const impact = [w.domains, files ? t('websites.deleteFilesImpact', { root: w.root }) : t('websites.deleteKeepImpact')]
  try {
    const ok = await withDanger(
      { level: files ? 2 : 1, action: t('websites.deleteSite'), node: hostInfo.label, impact, confirmText: t('common.delete') },
      (pw) => api.del(`${base}/sites/${w.id}`, { query: files ? { files: 1 } : undefined, confirmPassword: pw }).then(() => true),
    )
    if (ok) {
      load()
      emit('changed')
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

async function untakeover(w: Website) {
  try {
    const ok = await withDanger(
      { level: 1, action: t('websites.untakeover'), node: hostInfo.label, impact: [w.takeoverFrom, t('websites.untakeoverImpact')], confirmText: t('websites.untakeover') },
      () => api.post(`${base}/sites/${w.id}/untakeover`).then(() => true),
    )
    if (ok) {
      load()
      emit('changed')
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

/* ---------- 日志 ---------- */
const logOf = ref<Website | null>(null)
const logKind = ref<'access' | 'error'>('access')
const logLines = ref<string[]>([])
const logPath = ref('')
async function loadLog() {
  if (!logOf.value) return
  try {
    const r = await api.get<{ path: string; lines: string[] }>(`${base}/sites/${logOf.value.id}/log`, { kind: logKind.value, lines: 300 })
    logLines.value = r.lines
    logPath.value = r.path
  } catch (e) {
    message.error(errorText(e))
  }
}
watch([logOf, logKind], loadLog)

function moreOptions(w: Website) {
  const o = [{ key: 'log', label: t('websites.log') }]
  if (props.canManage) {
    if (w.takeoverFrom) o.push({ key: 'untakeover', label: t('websites.untakeover') })
    o.push({ key: 'delete', label: t('common.delete') })
    if (w.type === 'static' && !w.raw) o.push({ key: 'deleteFiles', label: t('websites.deleteWithFiles') })
  }
  return o
}
function onMore(w: Website, key: string) {
  if (key === 'log') logOf.value = w
  else if (key === 'untakeover') untakeover(w)
  else if (key === 'delete') remove(w, false)
  else if (key === 'deleteFiles') remove(w, true)
}

const columns = computed<DataTableColumns<Website>>(() => [
  {
    title: t('websites.site'),
    key: 'name',
    minWidth: 200,
    render: (w) =>
      h('div', { class: 'cell' }, [
        h('a', { href: url(w), target: '_blank', rel: 'noopener', class: 'dom' }, w.domains.split(' ').join('，')),
        h('div', { class: 'sd-muted small' }, [w.name, w.appName ? ' · ' + t('websites.fromApp', { app: w.appName }) : '', w.takeoverFrom ? ' · ' + t('websites.takenOver') : '', w.remark ? ' · ' + w.remark : '']),
      ]),
  },
  {
    title: t('websites.target'),
    key: 'type',
    minWidth: 200,
    render: (w) =>
      h('div', { class: 'cell' }, [
        h(NTag, { size: 'tiny', bordered: false }, () => (w.raw ? t('websites.raw') : t('websites.type.' + w.type))),
        h('span', { class: 'sd-mono small' }, w.raw ? '' : w.type === 'proxy' ? w.proxyPass : w.root),
      ]),
  },
  {
    title: 'HTTPS',
    key: 'https',
    width: 170,
    render: (w) => {
      if (!w.https) return h('span', { class: 'sd-muted' }, t('websites.httpOnly'))
      const days = w.certEnd ? Math.floor((Date.parse(w.certEnd) - Date.now()) / 86400000) : null
      return h('div', { class: 'cell' }, [
        h(NTag, { size: 'tiny', type: days !== null && days < 14 ? 'warning' : 'success', bordered: false }, () => (days !== null ? t('websites.daysLeft', { n: days }) : 'HTTPS')),
        h('span', { class: 'sd-muted small sd-ellipsis', title: w.certName }, w.certName ?? ''),
      ])
    },
  },
  {
    title: t('websites.enabled'),
    key: 'enabled',
    width: 70,
    render: (w) => h(NSwitch, { size: 'small', value: w.enabled, disabled: !props.canManage, 'onUpdate:value': (v: boolean) => toggle(w, v) }),
  },
  {
    title: '',
    key: 'actions',
    width: 130,
    render: (w) =>
      h('div', { class: 'acts' }, [
        props.canManage ? h(NButton, { size: 'tiny', quaternary: true, onClick: () => openForm(w), 'data-testid': 'site-edit-' + w.name }, () => t('common.edit')) : null,
        h(NDropdown, { trigger: 'click', options: moreOptions(w), onSelect: (k: string) => onMore(w, k) }, () =>
          h(NButton, { size: 'tiny', quaternary: true, 'data-testid': 'site-more-' + w.name }, () => '…'),
        ),
      ]),
  },
])
</script>

<template>
  <div class="sites">
    <div class="sd-toolbar">
      <span class="sd-muted small">{{ t('websites.sitesHint') }}</span>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <NButton v-if="canManage" size="small" type="primary" :disabled="!runtime.running && !runtime.mode" data-testid="site-add" @click="openForm()">{{ t('websites.add') }}</NButton>
    </div>
    <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="columns" :data="sites" :row-key="(w: Website) => w.id" :bordered="false" :row-props="(w: Website) => ({ style: '', 'data-testid': 'site-' + w.name })" />
    <SiteForm v-if="formSite" :site="formSite" :runtime="runtime" @close="formSite = null" @saved="saved" />
    <NDrawer :show="!!logOf" :width="760" placement="right" @update:show="(v: boolean) => !v && (logOf = null)">
      <NDrawerContent :title="t('websites.logOf', { name: logOf?.name ?? '' })" closable>
        <div class="logbar">
          <NRadioGroup v-model:value="logKind" size="small">
            <NRadioButton value="access">{{ t('websites.accessLog') }}</NRadioButton>
            <NRadioButton value="error">{{ t('websites.errorLog') }}</NRadioButton>
          </NRadioGroup>
          <span class="sd-muted small sd-mono">{{ logPath }}</span>
          <span style="flex: 1" />
          <NButton size="small" quaternary @click="loadLog">{{ t('explorer.refresh') }}</NButton>
        </div>
        <pre class="log sd-mono">{{ logLines.length ? logLines.join('\n') : t('websites.noLog') }}</pre>
      </NDrawerContent>
    </NDrawer>
  </div>
</template>

<style scoped>
.sites {
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
}
:deep(.dom) {
  color: var(--sd-accent);
  text-decoration: none;
}
:deep(.acts) {
  display: flex;
  gap: 4px;
}
.logbar {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 8px;
}
.log {
  margin: 0;
  font-size: 12px;
  white-space: pre-wrap;
  word-break: break-all;
  background: var(--sd-bg);
  padding: 8px;
  border-radius: 4px;
  min-height: 200px;
}
</style>
