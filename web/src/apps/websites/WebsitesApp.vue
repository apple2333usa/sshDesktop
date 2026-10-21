<script setup lang="ts">
// 网站：选择 Web 服务器（接管宿主机 Nginx / 安装面板的 OpenResty 容器），管理站点、证书，接管已有站点。
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NTabPane, NTabs, NTag, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { Task, WebRuntimeStatus } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useNotify } from '@/stores/notify'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import TaskLog from '@/components/TaskLog.vue'
import { errorText } from '@/i18n'
import SitesTab from './SitesTab.vue'
import CertsTab from './CertsTab.vue'
import ExistingTab from './ExistingTab.vue'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const notify = useNotify()
const hostInfo = useHost()
const canManage = computed(() => session.has('website:manage'))
const base = '/nodes/local/websites'

const tab = ref<string>((host.props.tab as string) || 'sites')
watch(tab, (v) => host.setState({ tab: v }))
// 从应用商店“创建网站”打开时切到站点页
watch(
  () => host.props.create,
  (v) => v && (tab.value = 'sites'),
  { immediate: true },
)

const rt = ref<WebRuntimeStatus | null>(null)
const logTask = ref<Task | null>(null)
async function loadRuntime() {
  try {
    rt.value = await api.get<WebRuntimeStatus>(base + '/runtime')
  } catch (e) {
    message.error(errorText(e))
  }
}
onMounted(loadRuntime)

async function setup(mode: 'host' | 'container') {
  const ports = Object.entries(rt.value?.ports ?? {})
    .map(([p, w]) => `${p}：${w}`)
    .join('，')
  try {
    const task = await withDanger(
      {
        level: 1,
        action: mode === 'host' ? t('websites.setupHost') : t('websites.setupContainer'),
        node: hostInfo.label,
        impact: mode === 'host' ? t('websites.setupHostImpact') : [t('websites.setupContainerImpact'), ports ? t('websites.portsBusy', { ports }) : ''].filter(Boolean),
        confirmText: mode === 'host' ? t('websites.takeover') : t('websites.install'),
      },
      () => api.post<Task>(base + '/runtime/setup', { mode }),
    )
    if (!task) return
    logTask.value = task
    await notify.waitTask(task)
    loadRuntime()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function control(action: string) {
  try {
    const ok = await withDanger(
      { level: 1, action: t('websites.ctl.' + action), node: hostInfo.label, impact: t('websites.ctlImpact'), confirmText: t('websites.ctl.' + action) },
      () => api.post(`${base}/runtime/action/${action}`).then(() => true),
    )
    if (ok) {
      message.success(t('websites.done'))
      loadRuntime()
    }
  } catch (e) {
    message.error(errorText(e))
  }
}
</script>

<template>
  <div class="sd-page websites" data-testid="websites">
    <template v-if="rt && !rt.mode">
      <div class="setup sd-scroll">
        <h3>{{ t('websites.chooseTitle') }}</h3>
        <p class="sd-muted">{{ t('websites.chooseIntro') }}</p>
        <div class="choices">
          <div class="choice" :class="{ off: !rt.hostBinary }">
            <b>{{ t('websites.hostTitle') }}</b>
            <p class="small">{{ rt.hostBinary ? t('websites.hostFound', { bin: rt.hostBinary }) : t('websites.hostMissing') }}</p>
            <p class="small sd-muted">{{ t('websites.hostDesc') }}</p>
            <NButton v-if="canManage" type="primary" :disabled="!rt.hostBinary" data-testid="web-setup-host" @click="setup('host')">{{ t('websites.takeover') }}</NButton>
          </div>
          <div class="choice" :class="{ off: !rt.docker }">
            <b>{{ t('websites.containerTitle') }}</b>
            <p class="small">{{ rt.docker ? t('websites.containerReady') : t('websites.dockerMissing') }}</p>
            <p class="small sd-muted">{{ t('websites.containerDesc') }}</p>
            <NButton v-if="canManage" :type="rt.hostBinary ? 'default' : 'primary'" :disabled="!rt.docker" data-testid="web-setup-container" @click="setup('container')">{{ t('websites.install') }}</NButton>
          </div>
        </div>
        <p v-if="Object.keys(rt.ports).length" class="small sd-muted">
          {{ t('websites.portsNow') }}
          <span v-for="(w, p) in rt.ports" :key="p" class="sd-mono"> {{ p }}：{{ w }}</span>
        </p>
      </div>
    </template>
    <template v-else-if="rt">
      <div class="sd-toolbar rt">
        <b>{{ rt.mode === 'host' ? t('websites.modeHost') : t('websites.modeContainer') }}</b>
        <span class="sd-muted small">{{ rt.runtime?.flavor }} {{ rt.runtime?.version }}</span>
        <NTag size="small" :type="rt.running ? 'success' : 'error'" :bordered="false" data-testid="web-running">{{ rt.running ? t('websites.running') : t('websites.stopped') }}</NTag>
        <span class="sd-muted small sd-mono sd-ellipsis">{{ rt.runtime?.conf }}</span>
        <span style="flex: 1" />
        <template v-if="canManage">
          <NButton size="tiny" quaternary @click="control('reload')">{{ t('websites.ctl.reload') }}</NButton>
          <NButton size="tiny" quaternary @click="control('restart')">{{ t('websites.ctl.restart') }}</NButton>
          <NButton v-if="rt.running" size="tiny" quaternary type="error" @click="control('stop')">{{ t('websites.ctl.stop') }}</NButton>
          <NButton v-else size="tiny" quaternary type="primary" @click="control('start')">{{ t('websites.ctl.start') }}</NButton>
        </template>
      </div>
      <NAlert v-if="rt.error" type="error" :bordered="false" class="note">{{ rt.error }}</NAlert>
      <NAlert v-else-if="rt.mode === 'host' && !rt.included" type="warning" :bordered="false" class="note">{{ t('websites.notIncluded') }}</NAlert>
      <NTabs v-model:value="tab" type="line" size="small" class="tabs">
        <NTabPane name="sites" :tab="t('websites.sites')">
          <SitesTab :runtime="rt" :can-manage="canManage" @changed="loadRuntime" />
        </NTabPane>
        <NTabPane name="certs" :tab="t('websites.certs')" display-directive="show:lazy">
          <CertsTab :can-manage="canManage" />
        </NTabPane>
        <NTabPane v-if="rt.mode === 'host'" name="existing" :tab="t('websites.existing')" display-directive="show:lazy">
          <ExistingTab :can-manage="canManage" @changed="loadRuntime" />
        </NTabPane>
      </NTabs>
    </template>
    <TaskLog v-model:task="logTask" />
  </div>
</template>

<style scoped>
.websites {
  background: var(--sd-surface);
}
.small {
  font-size: 12px;
}
.setup {
  padding: 18px 24px;
  overflow: auto;
}
.setup h3 {
  margin: 0 0 6px;
}
.choices {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 12px;
  margin: 14px 0;
}
.choice {
  border: 1px solid var(--sd-border);
  border-radius: 8px;
  padding: 12px 14px;
}
.choice.off {
  opacity: 0.6;
}
.choice p {
  margin: 6px 0;
}
.rt {
  border-bottom: 1px solid var(--sd-border);
}
.note {
  margin: 6px 10px 0;
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
</style>
