<script setup lang="ts">
// Docker：容器、Compose 项目、镜像、网络、存储卷。Docker 未安装时提供一键安装。只管理本机。
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NForm, NFormItem, NRadioButton, NRadioGroup, NSelect, NSpin, NTabPane, NTabs, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { DockerStatus, Task } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useNotify } from '@/stores/notify'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import TaskLog from '@/components/TaskLog.vue'
import { errorText, formatBytes } from '@/i18n'
import ContainersTab from './ContainersTab.vue'
import ComposeTab from './ComposeTab.vue'
import ImagesTab from './ImagesTab.vue'
import NetworksTab from './NetworksTab.vue'
import VolumesTab from './VolumesTab.vue'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const notify = useNotify()
const hostInfo = useHost()
const canManage = computed(() => session.has('docker:manage'))

const status = ref<DockerStatus | null>(null)
const loading = ref(false)
const tab = ref<string>((host.props.tab as string) || 'containers')
watch(tab, (v) => host.setState({ tab: v }))

async function load() {
  loading.value = true
  try {
    status.value = await api.get<DockerStatus>('/nodes/local/docker/status')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

/* ---------- 安装与启动 ---------- */
const method = ref<'script' | 'package'>('script')
const mirror = ref('')
const logTask = ref<Task | null>(null)
const busy = ref(false)

async function install() {
  try {
    const ok = await withDanger(
      {
        level: 1,
        action: t('docker.installAction'),
        node: hostInfo.label,
        impact: [method.value === 'script' ? t('docker.installScriptImpact') : t('docker.installPackageImpact')],
        confirmText: t('docker.install'),
      },
      async () => true,
    )
    if (!ok) return
    busy.value = true
    const task = await api.post<Task>('/nodes/local/docker/install', { method: method.value, mirror: mirror.value })
    logTask.value = task
    const done = await notify.waitTask(task)
    if (done.status === 'success') load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    busy.value = false
  }
}

async function service(action: 'start' | 'restart' | 'stop') {
  busy.value = true
  try {
    const run = () => api.post(`/nodes/local/docker/service/${action}`)
    const r =
      action === 'start'
        ? await run()
        : await withDanger(
            { level: 1, action: t('docker.serviceAction', { action: t('services.' + action) }), node: hostInfo.label, impact: t('docker.serviceImpact'), confirmText: t('services.' + action) },
            run,
          )
    if (r !== undefined) await load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    busy.value = false
  }
}

const info = computed(() => status.value?.info)
</script>

<template>
  <div class="sd-page docker" data-testid="docker">
    <NSpin v-if="!status" :show="loading" class="center" />
    <div v-else-if="!status.running" class="setup">
      <NAlert :type="status.installed ? 'warning' : 'info'" :bordered="false" :title="status.installed ? t('docker.notRunning') : t('docker.notInstalled')">
        {{ status.installed ? t('docker.notRunningHint') : t('docker.notInstalledHint') }}
        <div v-if="status.error" class="sd-muted small sd-mono">{{ status.error }}</div>
      </NAlert>
      <template v-if="canManage">
        <div v-if="status.installed" class="ops">
          <NButton type="primary" :loading="busy" data-testid="docker-start" @click="service('start')">{{ t('docker.startService') }}</NButton>
          <NButton @click="load">{{ t('explorer.refresh') }}</NButton>
        </div>
        <NForm v-else label-placement="left" label-width="96" size="small" class="form">
          <NFormItem :label="t('docker.method')">
            <NRadioGroup v-model:value="method" size="small">
              <NRadioButton value="script">{{ t('docker.methodScript') }}</NRadioButton>
              <NRadioButton value="package">{{ t('docker.methodPackage') }}</NRadioButton>
            </NRadioGroup>
          </NFormItem>
          <NFormItem :label="t('docker.mirror')">
            <NSelect
              v-model:value="mirror"
              :options="[
                { label: t('docker.mirrorOfficial'), value: '' },
                { label: t('docker.mirrorAliyun'), value: 'Aliyun' },
                { label: t('docker.mirrorAzure'), value: 'AzureChinaCloud' },
              ]"
              style="width: 240px"
            />
          </NFormItem>
          <NFormItem label=" ">
            <NButton type="primary" :loading="busy" data-testid="docker-install" @click="install">{{ t('docker.install') }}</NButton>
          </NFormItem>
        </NForm>
      </template>
    </div>
    <NTabs v-else v-model:value="tab" type="line" size="small" class="tabs" animated>
      <template #suffix>
        <span class="sd-muted small ver">Docker {{ status.version }}<template v-if="status.compose"> · Compose {{ status.compose }}</template></span>
      </template>
      <NTabPane name="containers" :tab="t('docker.containers')" display-directive="show:lazy">
        <ContainersTab :active="tab === 'containers' && host.visible.value" />
      </NTabPane>
      <NTabPane name="compose" :tab="t('docker.compose')" display-directive="show:lazy">
        <ComposeTab :active="tab === 'compose' && host.visible.value" :compose="!!status.compose" />
      </NTabPane>
      <NTabPane name="images" :tab="t('docker.images')" display-directive="show:lazy">
        <ImagesTab />
      </NTabPane>
      <NTabPane name="networks" :tab="t('docker.networks')" display-directive="show:lazy">
        <NetworksTab />
      </NTabPane>
      <NTabPane name="volumes" :tab="t('docker.volumes')" display-directive="show:lazy">
        <VolumesTab />
      </NTabPane>
      <NTabPane name="info" :tab="t('docker.overview')">
        <div class="info sd-scroll">
          <dl v-if="info" class="kv">
            <dt>{{ t('docker.version') }}</dt>
            <dd>{{ status.version }}（API {{ status.apiVersion }}）</dd>
            <dt>Compose</dt>
            <dd>{{ status.compose || t('docker.noCompose') }}</dd>
            <dt>{{ t('docker.containers') }}</dt>
            <dd>{{ t('docker.containerCount', { n: info.Containers, running: info.ContainersRunning, stopped: info.ContainersStopped }) }}</dd>
            <dt>{{ t('docker.images') }}</dt>
            <dd>{{ info.Images }}</dd>
            <dt>{{ t('docker.storageDriver') }}</dt>
            <dd>{{ info.Driver }}</dd>
            <dt>{{ t('docker.rootDir') }}</dt>
            <dd class="sd-mono">{{ info.DockerRootDir }}</dd>
            <dt>{{ t('docker.system') }}</dt>
            <dd>{{ info.OperatingSystem }} · {{ info.NCPU }} CPU · {{ formatBytes(info.MemTotal) }}</dd>
            <dt>Socket</dt>
            <dd class="sd-mono">{{ status.socket }}</dd>
          </dl>
          <div v-if="canManage" class="ops">
            <NButton size="small" :loading="busy" @click="service('restart')">{{ t('docker.restartService') }}</NButton>
            <NButton size="small" type="error" secondary :loading="busy" @click="service('stop')">{{ t('docker.stopService') }}</NButton>
          </div>
        </div>
      </NTabPane>
    </NTabs>
    <TaskLog v-model:task="logTask" />
  </div>
</template>

<style scoped>
.docker {
  background: var(--sd-surface);
}
.center {
  margin: 60px auto;
}
.setup {
  padding: 16px;
  max-width: 640px;
}
.form {
  margin-top: 16px;
}
.ops {
  display: flex;
  gap: 8px;
  margin-top: 14px;
}
.small {
  font-size: 12px;
}
.ver {
  padding-right: 12px;
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
.info {
  padding: 12px 16px;
  font-size: 13px;
}
.kv {
  display: grid;
  grid-template-columns: 100px 1fr;
  gap: 6px 8px;
  margin: 0;
}
.kv dt {
  color: var(--sd-text-2);
}
.kv dd {
  margin: 0;
  word-break: break-all;
}
</style>
