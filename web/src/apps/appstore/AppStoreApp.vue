<script setup lang="ts">
// 应用商店：浏览应用源（默认兼容 1Panel 应用商店）中的应用并一键安装；管理已安装的应用和应用源。
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NAlert, NBadge, NButton, NEmpty, NInput, NModal, NSelect, NSpin, NSwitch, NTabPane, NTabs, NTag, NTooltip, useMessage,
} from 'naive-ui'
import { api } from '@/api/http'
import type { StoreApp, StoreSource, StoreTag, Task } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useNotify } from '@/stores/notify'
import TaskLog from '@/components/TaskLog.vue'
import { errorText, formatTime } from '@/i18n'
import InstallDialog from './InstallDialog.vue'
import InstalledTab from './InstalledTab.vue'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const notify = useNotify()
const canManage = computed(() => session.has('appstore:manage'))

const tab = ref<'apps' | 'installed'>((host.props.tab as 'apps' | 'installed') || 'apps')
watch(tab, (v) => host.setState({ tab: v }))

const apps = ref<StoreApp[]>([])
const tags = ref<StoreTag[]>([])
const errors = ref<Record<string, string>>({})
const syncing = ref(false)
const loading = ref(false)
const q = ref('')
const tag = ref<string | null>(null)
const logTask = ref<Task | null>(null)
const installedRef = ref<InstanceType<typeof InstalledTab> | null>(null)

async function load() {
  loading.value = true
  try {
    const r = await api.get<{ apps: StoreApp[]; tags: StoreTag[]; errors: Record<string, string>; syncing: boolean }>('/nodes/local/appstore/apps')
    apps.value = r.apps
    tags.value = r.tags
    errors.value = r.errors
    syncing.value = r.syncing
    // 首次打开时后台正在同步，稍后再取
    if (r.syncing) setTimeout(load, 3000)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  return apps.value.filter((a) => {
    if (tag.value && !a.tags.includes(tag.value)) return false
    return !s || a.name.toLowerCase().includes(s) || a.key.toLowerCase().includes(s) || a.description.toLowerCase().includes(s)
  })
})

async function sync() {
  try {
    const task = await api.post<Task>('/nodes/local/appstore/sync')
    syncing.value = true
    logTask.value = task
    await notify.waitTask(task)
  } catch (e) {
    message.error(errorText(e))
  }
  syncing.value = false
  load()
}

/* ---------- 详情与安装 ---------- */
const detail = ref<StoreApp | null>(null)
const readme = ref('')
async function openDetail(a: StoreApp) {
  detail.value = a
  readme.value = ''
  try {
    const r = await api.get<{ readme: string }>(`/nodes/local/appstore/apps/${a.source}/${a.key}`)
    readme.value = r.readme
  } catch {
    /* 忽略 */
  }
}
// 去掉 Markdown 标记，按纯文本显示说明
const plainReadme = computed(() =>
  readme.value
    .replace(/!\[[^\]]*\]\([^)]*\)/g, '')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/^#+\s*/gm, '')
    .replace(/\*\*([^*]+)\*\*/g, '$1')
    .replace(/\n{3,}/g, '\n\n')
    .trim(),
)

const installing = ref<StoreApp | null>(null)
const editing = ref<string | null>(null)
function install(a: StoreApp) {
  detail.value = null
  installing.value = a
}

async function onTask(task: Task) {
  logTask.value = task
  tab.value = 'installed'
  await notify.waitTask(task)
  installedRef.value?.load()
  load()
}

function tagName(key: string) {
  return tags.value.find((x) => x.key === key)?.name ?? key
}

/* ---------- 应用源 ---------- */
const showSources = ref(false)
const sources = ref<StoreSource[]>([])
async function openSources() {
  try {
    sources.value = await api.get<StoreSource[]>('/nodes/local/appstore/sources')
    showSources.value = true
  } catch (e) {
    message.error(errorText(e))
  }
}
function addSource() {
  sources.value.push({ id: '', name: '', type: 'git', url: '', enabled: true, builtin: false })
}
async function saveSources() {
  try {
    await api.put('/nodes/local/appstore/sources', sources.value)
    showSources.value = false
    message.success(t('common.saved'))
    sync()
  } catch (e) {
    message.error(errorText(e))
  }
}
// 写在模板里的 // 会被 vue-tsc 当成注释
const urlPlaceholder = 'https://…'
</script>

<template>
  <div class="sd-page store" data-testid="appstore">
    <NTabs v-model:value="tab" type="line" size="small" class="tabs">
      <NTabPane name="apps" :tab="t('appstore.all')">
        <div class="sd-toolbar">
          <NInput v-model:value="q" size="small" clearable :placeholder="t('appstore.search')" style="max-width: 220px" data-testid="app-search" />
          <NSelect v-model:value="tag" size="small" clearable :placeholder="t('appstore.allTags')" :options="tags.map((x) => ({ label: x.name, value: x.key }))" style="width: 140px" />
          <span class="sd-muted small">{{ t('appstore.count', { n: shown.length }) }}</span>
          <span style="flex: 1" />
          <template v-if="canManage">
            <NButton size="small" quaternary @click="openSources">{{ t('appstore.sources') }}</NButton>
            <NButton size="small" :loading="syncing" data-testid="app-sync" @click="sync">{{ t('appstore.sync') }}</NButton>
          </template>
        </div>
        <NAlert v-for="(msg, id) in errors" :key="id" type="warning" :bordered="false" class="err">{{ t('appstore.sourceError', { id, msg }) }}</NAlert>
        <NSpin :show="loading && !apps.length" class="gridwrap">
          <div class="grid sd-scroll">
            <div v-for="a in shown" :key="a.source + '/' + a.key" class="card" :data-testid="'app-' + a.key" @click="openDetail(a)">
              <img class="icon" :src="a.icon" loading="lazy" alt="" />
              <div class="meta">
                <div class="row1">
                  <b class="sd-ellipsis">{{ a.name }}</b>
                  <NBadge v-if="a.installed" :value="t('appstore.installedN', { n: a.installed })" type="success" />
                </div>
                <div class="desc sd-muted">{{ a.description }}</div>
                <div class="row2">
                  <NTag v-for="x in a.tags.slice(0, 2)" :key="x" size="tiny" :bordered="false">{{ tagName(x) }}</NTag>
                  <span style="flex: 1" />
                  <NTooltip v-if="a.unsupported">
                    <template #trigger>
                      <NTag size="tiny" type="warning" :bordered="false">{{ t('appstore.unsupported') }}</NTag>
                    </template>
                    {{ a.unsupported }}
                  </NTooltip>
                  <NButton
                    v-else-if="canManage"
                    size="tiny"
                    type="primary"
                    secondary
                    :disabled="a.limit > 0 && a.installed >= a.limit"
                    :data-testid="'app-install-' + a.key"
                    @click.stop="install(a)"
                  >
                    {{ t('appstore.install') }}
                  </NButton>
                </div>
              </div>
            </div>
            <NEmpty v-if="!shown.length && !loading" class="empty" :description="syncing ? t('appstore.syncing') : t('appstore.empty')" />
          </div>
        </NSpin>
      </NTabPane>
      <NTabPane name="installed" :tab="t('appstore.installed')" display-directive="show:lazy">
        <InstalledTab ref="installedRef" :active="tab === 'installed' && host.visible.value" @task="onTask" @params="(n: string) => (editing = n)" />
      </NTabPane>
    </NTabs>

    <NModal :show="!!detail" preset="card" :title="detail?.name" style="width: 640px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && (detail = null)">
      <template v-if="detail">
        <div class="dhead">
          <img class="icon big" :src="detail.icon" alt="" />
          <div class="meta">
            <div>{{ detail.description }}</div>
            <div class="links small">
              <a v-if="detail.website" :href="detail.website" target="_blank" rel="noopener noreferrer">{{ t('appstore.website') }}</a>
              <a v-if="detail.github" :href="detail.github" target="_blank" rel="noopener noreferrer">GitHub</a>
              <a v-if="detail.document" :href="detail.document" target="_blank" rel="noopener noreferrer">{{ t('appstore.document') }}</a>
              <span class="sd-muted">{{ t('appstore.versions') }}：{{ detail.versions.slice(0, 5).join('、') }}</span>
              <span v-if="detail.architectures.length" class="sd-muted">{{ detail.architectures.join(' / ') }}</span>
            </div>
          </div>
        </div>
        <NAlert v-if="detail.unsupported" type="warning" :bordered="false" class="err">{{ detail.unsupported }}</NAlert>
        <pre class="readme sd-scroll">{{ plainReadme || t('common.loading') }}</pre>
      </template>
      <template #footer>
        <div class="foot">
          <NButton @click="detail = null">{{ t('common.close') }}</NButton>
          <NButton v-if="canManage && detail && !detail.unsupported" type="primary" data-testid="app-detail-install" @click="install(detail)">{{ t('appstore.install') }}</NButton>
        </div>
      </template>
    </NModal>

    <InstallDialog :app="installing" :installed="editing" @close="(installing = null), (editing = null)" @task="onTask" />

    <NModal v-model:show="showSources" preset="card" :title="t('appstore.sources')" style="width: 720px; max-width: calc(100vw - 32px)">
      <div class="sources">
        <div v-for="(s, i) in sources" :key="i" class="src">
          <NSwitch v-model:value="s.enabled" size="small" />
          <template v-if="s.builtin">
            <b class="sname">{{ s.name }}</b>
            <NInput v-model:value="s.url" size="small" :placeholder="t('appstore.indexUrl')" />
          </template>
          <template v-else>
            <NInput v-model:value="s.name" size="small" :placeholder="t('appstore.sourceName')" style="width: 140px" />
            <NSelect
              v-model:value="s.type"
              size="small"
              style="width: 110px"
              :options="[
                { label: 'Git', value: 'git' },
                { label: t('appstore.typeDir'), value: 'dir' },
                { label: t('appstore.typeIndex'), value: 'index' },
              ]"
            />
            <NInput v-model:value="s.url" size="small" :placeholder="s.type === 'dir' ? '/opt/my-apps' : urlPlaceholder" />
            <NButton size="tiny" quaternary @click="sources.splice(i, 1)">×</NButton>
          </template>
          <div v-if="s.error || s.apps !== undefined" class="sd-muted small sstat">
            {{ s.error ? s.error : t('appstore.sourceStat', { n: s.apps ?? 0 }) }}<template v-if="s.synced"> · {{ t('appstore.synced', { time: formatTime(s.synced * 1000) }) }}</template>
          </div>
        </div>
        <NButton size="small" dashed @click="addSource">{{ t('appstore.addSource') }}</NButton>
        <div class="sd-muted small">{{ t('appstore.sourceHint') }}</div>
      </div>
      <template #footer>
        <div class="foot">
          <NButton @click="showSources = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" @click="saveSources">{{ t('appstore.saveAndSync') }}</NButton>
        </div>
      </template>
    </NModal>
    <TaskLog v-model:task="logTask" />
  </div>
</template>

<style scoped>
.store {
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
.small {
  font-size: 12px;
}
.err {
  margin: 8px 10px 0;
}
.gridwrap {
  flex: 1;
  min-height: 0;
}
.gridwrap :deep(.n-spin-content) {
  height: 100%;
}
.grid {
  height: 100%;
  padding: 10px;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(260px, 1fr));
  grid-auto-rows: min-content;
  gap: 10px;
}
.card {
  display: flex;
  gap: 10px;
  padding: 10px;
  border-radius: var(--sd-radius);
  border: 1px solid var(--sd-border);
  background: var(--sd-surface-2);
  cursor: pointer;
  min-width: 0;
}
.card:hover {
  background: var(--sd-hover);
}
.icon {
  width: 44px;
  height: 44px;
  border-radius: 8px;
  object-fit: contain;
  flex: none;
  background: #fff;
}
.icon.big {
  width: 64px;
  height: 64px;
}
.meta {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.row1,
.row2 {
  display: flex;
  gap: 6px;
  align-items: center;
  min-width: 0;
}
.desc {
  font-size: 12px;
  line-height: 1.4;
  height: 2.8em;
  overflow: hidden;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}
.empty {
  grid-column: 1 / -1;
  margin-top: 40px;
}
.dhead {
  display: flex;
  gap: 12px;
  margin-bottom: 10px;
}
.links {
  display: flex;
  gap: 10px;
  flex-wrap: wrap;
}
.links a {
  color: var(--sd-accent);
}
.readme {
  max-height: 50vh;
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: var(--sd-font);
  font-size: 13px;
  line-height: 1.6;
}
.sources {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.src {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.src > .n-input:last-of-type {
  flex: 1;
  min-width: 200px;
}
.sname {
  width: 140px;
}
.sstat {
  width: 100%;
  padding-left: 48px;
}
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
</style>
