<script setup lang="ts">
// 已安装的应用：状态、端口，启动 / 停止 / 重启 / 重建，修改参数、升级、日志、卸载。
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCheckbox, NDropdown, NEmpty, NModal, NSelect, NSpin, NTag, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { InstalledApp, Task } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import LogStream from '@/components/LogStream.vue'
import { errorText, formatTime } from '@/i18n'

const props = defineProps<{ active: boolean }>()
const emit = defineEmits<{ task: [Task]; params: [string] }>()
const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const hostInfo = useHost()
const canManage = computed(() => session.has('appstore:manage'))
const base = '/nodes/local/appstore/installed'

const list = ref<InstalledApp[]>([])
const loading = ref(false)
let timer: number | undefined

async function load() {
  loading.value = true
  try {
    list.value = await api.get<InstalledApp[]>(base)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
defineExpose({ load })
onMounted(() => {
  load()
  timer = window.setInterval(() => props.active && load(), 6000)
})
onUnmounted(() => clearInterval(timer))
watch(() => props.active, (v) => v && load())

const stateType = (s: string) =>
  (({ running: 'success', partial: 'warning', stopped: 'default', installing: 'info', upgrading: 'info', failed: 'error' }) as Record<string, 'success' | 'warning' | 'default' | 'info' | 'error'>)[s] ?? 'default'

async function control(a: InstalledApp, action: string) {
  try {
    const run = () => api.post<Task>(`${base}/${a.name}/${action}`)
    const task = action === 'start'
      ? await run()
      : await withDanger(
          { level: 1, action: t('appstore.controlAction', { action: t('appstore.act.' + action) }), node: hostInfo.label, impact: `${a.title}（${a.name}）`, confirmText: t('appstore.act.' + action) },
          run,
        )
    if (task) emit('task', task)
  } catch (e) {
    message.error(errorText(e))
  }
}

/* ---------- 升级 ---------- */
const upgrading = ref<InstalledApp | null>(null)
const target = ref('')
function openUpgrade(a: InstalledApp) {
  upgrading.value = a
  target.value = a.upgrades[0] ?? ''
}
async function upgrade() {
  const a = upgrading.value
  if (!a) return
  upgrading.value = null
  try {
    const task = await withDanger(
      {
        level: 1,
        action: t('appstore.upgradeAction'),
        node: hostInfo.label,
        impact: [`${a.title}（${a.name}）：${a.version} → ${target.value}`, t('appstore.upgradeImpact')],
        confirmText: t('appstore.upgrade'),
      },
      () => api.post<Task>(`${base}/${a.name}/upgrade`, { version: target.value }),
    )
    if (task) emit('task', task)
  } catch (e) {
    message.error(errorText(e))
  }
}

/* ---------- 卸载 ---------- */
const removing = ref<InstalledApp | null>(null)
const deleteData = ref(false)
async function uninstall() {
  const a = removing.value
  if (!a) return
  removing.value = null
  try {
    const task = await withDanger(
      {
        level: deleteData.value ? 2 : 1,
        action: t('appstore.uninstallAction'),
        node: hostInfo.label,
        impact: [`${a.title}（${a.name}）`, deleteData.value ? t('appstore.uninstallData', { dir: a.dir }) : t('appstore.uninstallKeep', { dir: a.dir })],
        confirmText: t('appstore.uninstallNamed', { name: a.name }),
      },
      (pw) => api.del<Task>(`${base}/${a.name}`, { query: { data: deleteData.value ? '1' : undefined }, confirmPassword: pw }),
    )
    if (task) emit('task', task)
  } catch (e) {
    message.error(errorText(e))
  }
}

/* ---------- 日志 ---------- */
const logsOf = ref<InstalledApp | null>(null)

function more(a: InstalledApp) {
  return [
    { label: t('docker.logs'), key: 'logs' },
    { label: t('appstore.openDir'), key: 'dir', disabled: !session.has('files:view') },
    ...(session.has('website:manage') ? [{ label: t('appstore.createSite'), key: 'site', disabled: !a.ports.some((p) => /^\d+$/.test(p)) }] : []),
    ...(canManage.value
      ? [
          { label: t('appstore.params'), key: 'params', disabled: a.busy },
          { label: t('appstore.act.rebuild'), key: 'rebuild', disabled: a.busy },
          { label: t('appstore.upgrade'), key: 'upgrade', disabled: a.busy || !a.upgrades.length },
          { type: 'divider', key: 'd' },
          { label: t('appstore.uninstall'), key: 'uninstall', disabled: a.busy },
        ]
      : []),
  ]
}

function onMore(a: InstalledApp, key: string) {
  if (key === 'logs') logsOf.value = a
  else if (key === 'dir') host.openApp('explorer', { node: 'local', path: a.dir })
  else if (key === 'site') host.openApp('websites', { create: { appName: a.name, port: a.ports.find((p) => /^\d+$/.test(p)), title: a.title } })
  else if (key === 'params') emit('params', a.name)
  else if (key === 'upgrade') openUpgrade(a)
  else if (key === 'uninstall') {
    deleteData.value = false
    removing.value = a
  } else control(a, key)
}

// 允许外部访问时，端口给出可点击的地址
function portLink(a: InstalledApp, p: string) {
  if (!a.external || !/^\d+$/.test(p)) return null
  return `http://${location.hostname}:${p}`
}
</script>

<template>
  <div class="inst">
    <div class="sd-toolbar">
      <b>{{ t('appstore.installedCount', { n: list.length }) }}</b>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
    </div>
    <NSpin :show="loading && !list.length" class="wrap">
      <div class="list sd-scroll">
        <div v-for="a in list" :key="a.name" class="item" :data-testid="'inst-' + a.name">
          <img class="icon" :src="a.icon" alt="" />
          <div class="meta">
            <div class="row1">
              <b>{{ a.title }}</b>
              <span class="sd-mono sd-muted">{{ a.name }}</span>
              <NTag size="tiny" :bordered="false">{{ a.version }}</NTag>
              <NTag size="small" :type="stateType(a.state)" :bordered="false" :data-testid="'inst-state-' + a.name">
                {{ t('appstore.state.' + a.state, a.state) }}<template v-if="a.total"> {{ a.running }}/{{ a.total }}</template>
              </NTag>
              <NTag v-if="a.upgrades.length" size="tiny" type="info" :bordered="false">{{ t('appstore.canUpgrade', { v: a.upgrades[0] }) }}</NTag>
            </div>
            <div class="row2 small">
              <template v-for="p in a.ports" :key="p">
                <a v-if="portLink(a, p)" :href="portLink(a, p)!" target="_blank" rel="noopener noreferrer" class="sd-mono">{{ p }}</a>
                <span v-else class="sd-mono sd-muted">{{ t('appstore.localPort', { p }) }}</span>
              </template>
              <span v-if="!a.ports.length" class="sd-muted">{{ t('appstore.noPorts') }}</span>
              <span v-if="a.sites.length" class="sd-muted">· {{ t('appstore.sites', { names: a.sites.join('，') }) }}</span>
              <span class="sd-muted">· {{ t('appstore.installedAt', { time: formatTime(a.createdAt) }) }}</span>
            </div>
            <NAlert v-if="a.message && (a.state === 'failed' || a.status === 'failed')" type="error" :bordered="false" class="msg">{{ a.message }}</NAlert>
          </div>
          <div class="acts">
            <template v-if="canManage">
              <NButton v-if="a.state !== 'running'" size="tiny" secondary :disabled="a.busy" :data-testid="'inst-start-' + a.name" @click="control(a, 'start')">{{ t('appstore.act.start') }}</NButton>
              <template v-else>
                <NButton size="tiny" secondary :disabled="a.busy" @click="control(a, 'restart')">{{ t('appstore.act.restart') }}</NButton>
                <NButton size="tiny" secondary type="error" :disabled="a.busy" :data-testid="'inst-stop-' + a.name" @click="control(a, 'stop')">{{ t('appstore.act.stop') }}</NButton>
              </template>
            </template>
            <NDropdown trigger="click" :options="more(a)" @select="(k: string) => onMore(a, k)">
              <NButton size="tiny" quaternary :data-testid="'inst-more-' + a.name">⋯</NButton>
            </NDropdown>
          </div>
        </div>
        <NEmpty v-if="!list.length && !loading" class="empty" :description="t('appstore.noInstalled')" />
      </div>
    </NSpin>

    <NModal :show="!!upgrading" preset="card" :title="t('appstore.upgrade') + ' · ' + (upgrading?.title ?? '')" style="width: 420px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && (upgrading = null)">
      <NSelect v-model:value="target" :options="(upgrading?.upgrades ?? []).map((v) => ({ label: v, value: v }))" />
      <div class="sd-muted small hint">{{ t('appstore.upgradeHint') }}</div>
      <template #footer>
        <div class="foot">
          <NButton @click="upgrading = null">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :disabled="!target" @click="upgrade">{{ t('appstore.upgrade') }}</NButton>
        </div>
      </template>
    </NModal>

    <NModal :show="!!removing" preset="card" :title="t('appstore.uninstall') + ' · ' + (removing?.title ?? '')" style="width: 440px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && (removing = null)">
      <NCheckbox v-model:checked="deleteData" data-testid="inst-delete-data">{{ t('appstore.deleteData') }}</NCheckbox>
      <div class="sd-muted small hint">{{ t('appstore.deleteDataHint') }}</div>
      <template #footer>
        <div class="foot">
          <NButton @click="removing = null">{{ t('common.cancel') }}</NButton>
          <NButton type="error" data-testid="inst-uninstall-ok" @click="uninstall">{{ t('appstore.uninstall') }}</NButton>
        </div>
      </template>
    </NModal>

    <NModal :show="!!logsOf" preset="card" :title="t('docker.logs') + ' · ' + (logsOf?.name ?? '')" style="width: 860px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && (logsOf = null)">
      <div class="logbox">
        <LogStream v-if="logsOf" :path="`${base}/${logsOf.name}/logs?tail=300`" />
      </div>
    </NModal>
  </div>
</template>

<style scoped>
.inst {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.wrap {
  flex: 1;
  min-height: 0;
}
.wrap :deep(.n-spin-content) {
  height: 100%;
}
.list {
  height: 100%;
  padding: 8px 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.item {
  display: flex;
  gap: 10px;
  padding: 10px;
  border: 1px solid var(--sd-border);
  border-radius: var(--sd-radius);
  background: var(--sd-surface-2);
  align-items: flex-start;
}
.icon {
  width: 40px;
  height: 40px;
  border-radius: 8px;
  object-fit: contain;
  background: #fff;
  flex: none;
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
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.row2 a {
  color: var(--sd-accent);
}
.small {
  font-size: 12px;
}
.msg {
  margin-top: 4px;
  font-size: 12px;
}
.acts {
  display: flex;
  gap: 4px;
  align-items: center;
  flex: none;
}
.empty {
  margin-top: 40px;
}
.hint {
  margin-top: 8px;
}
.logbox {
  height: 60vh;
  display: flex;
  flex-direction: column;
}
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
</style>
