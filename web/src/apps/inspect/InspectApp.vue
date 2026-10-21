<script setup lang="ts">
// 环境体检：检查服务器上已有的 Web 服务器、证书、Docker、防火墙、计划任务、SSH 等，说明面板能接管到什么程度。
// 首次使用时由管理员登录自动打开并执行。
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NEmpty, NSpin, NTag, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { InspectItem, InspectReport } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { appMap } from '@/apps/registry'
import { useSession } from '@/stores/session'
import { errorText, formatTime } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const report = ref<InspectReport | null>(null)
const running = ref(false)

const stateType: Record<InspectItem['state'], 'success' | 'warning' | 'error' | 'info' | 'default'> = {
  managed: 'success',
  partial: 'warning',
  unmanaged: 'default',
  info: 'info',
  warning: 'error',
}

async function run() {
  running.value = true
  try {
    report.value = await api.post<InspectReport>('/nodes/local/inspect/run')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    running.value = false
  }
}

onMounted(async () => {
  try {
    report.value = await api.get<InspectReport | null>('/nodes/local/inspect')
  } catch (e) {
    message.error(errorText(e))
  }
  if (!report.value || host.props.run) run()
})

function canOpen(app?: string) {
  const a = app ? appMap[app] : undefined
  return !!a && (!a.permission || session.has(a.permission))
}
</script>

<template>
  <div class="sd-page inspect" data-testid="inspect">
    <div class="sd-toolbar">
      <span class="sd-muted small">{{ report ? t('inspect.at', { time: formatTime(report.at) }) : t('inspect.intro') }}</span>
      <span style="flex: 1" />
      <NButton size="small" type="primary" :loading="running" data-testid="inspect-run" @click="run">{{ t('inspect.run') }}</NButton>
    </div>
    <div class="list sd-scroll">
      <NSpin v-if="running && !report" />
      <NEmpty v-else-if="!report" :description="t('inspect.empty')" />
      <div v-for="it in report?.items ?? []" :key="it.key" class="item" :data-testid="'inspect-' + it.key">
        <div class="head">
          <b>{{ it.title }}</b>
          <NTag size="small" :type="stateType[it.state]" :bordered="false">{{ t('inspect.state.' + it.state) }}</NTag>
          <span style="flex: 1" />
          <NButton v-if="canOpen(it.app)" size="tiny" secondary @click="host.openApp(it.app!)">{{ t('inspect.open', { app: t(appMap[it.app!].title) }) }}</NButton>
        </div>
        <div class="summary">{{ it.summary }}</div>
        <ul v-if="it.notes.length" class="notes sd-muted">
          <li v-for="n in it.notes" :key="n">{{ n }}</li>
        </ul>
        <ul v-if="it.actions.length" class="actions">
          <li v-for="a in it.actions" :key="a">{{ a }}</li>
        </ul>
      </div>
    </div>
  </div>
</template>

<style scoped>
.inspect {
  background: var(--sd-surface);
}
.small {
  font-size: 12px;
}
.list {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 4px 12px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.item {
  border: 1px solid var(--sd-border);
  border-radius: 6px;
  padding: 8px 12px;
}
.head {
  display: flex;
  gap: 8px;
  align-items: center;
}
.summary {
  margin-top: 4px;
}
.notes,
.actions {
  margin: 4px 0 0;
  padding-left: 18px;
  font-size: 12px;
}
.actions {
  color: var(--sd-accent, #0067c0);
}
</style>
