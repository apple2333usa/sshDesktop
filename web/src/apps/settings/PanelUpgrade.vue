<script setup lang="ts">
// 面板升级：检查 GitHub 上的最新版本，一键升级（后端在独立的 systemd 单元里执行 sshdesktop upgrade，失败自动回滚）。
// 升级期间面板会重启，这里轮询健康检查，版本变化后刷新页面。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NInput, NTag, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { UpgradeStatus } from '@/api/types'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import { errorText, formatTime } from '@/i18n'

const { t } = useI18n()
const message = useMessage()
const hostInfo = useHost()

const st = ref<UpgradeStatus | null>(null)
const checking = ref(false)
const mirror = ref('')
// 升级进行中：目标版本；结果：done / failed
const target = ref('')
const result = ref<'' | 'done' | 'failed'>('')
const log = ref('')
let timer: ReturnType<typeof setTimeout> | undefined

async function load(refresh = false) {
  checking.value = true
  try {
    st.value = await api.get<UpgradeStatus>('/system/upgrade', refresh ? { refresh: 1 } : undefined)
    if (st.value.running && !target.value) {
      target.value = st.value.latest?.tag ?? '?'
      poll(Date.now())
    }
  } catch (e) {
    message.error(errorText(e))
  } finally {
    checking.value = false
  }
}
onMounted(() => load())
onBeforeUnmount(() => clearTimeout(timer))

const latest = computed(() => st.value?.latest)

async function upgrade() {
  const tag = latest.value?.tag
  if (!tag) return
  try {
    const ok = await withDanger(
      {
        level: 2,
        action: t('upgrade.action', { from: st.value?.current, to: tag }),
        node: hostInfo.label,
        impact: [t('upgrade.impact1'), t('upgrade.impact2')],
        confirmText: t('upgrade.confirm', { v: tag }),
      },
      (pw) => api.post('/system/upgrade', { version: tag, mirror: mirror.value.trim() }, { confirmPassword: pw }).then(() => true),
    )
    if (!ok) return
    target.value = tag
    result.value = ''
    poll(Date.now())
  } catch (e) {
    message.error(errorText(e))
  }
}

/** 轮询：面板重启期间请求会失败，忽略；版本变成目标版本后刷新页面；升级单元结束但版本没变则判为失败。 */
function poll(started: number) {
  timer = setTimeout(async () => {
    try {
      const h = await api.get<{ version: string }>('/health')
      if ('v' + h.version === target.value) {
        result.value = 'done'
        message.success(t('upgrade.done', { v: target.value }))
        setTimeout(() => location.reload(), 2000)
        return
      }
      const s = await api.get<UpgradeStatus>('/system/upgrade')
      log.value = s.log ?? ''
      if (!s.running && Date.now() - started > 8000) {
        result.value = 'failed'
        st.value = s
        return
      }
    } catch {
      /* 面板正在重启 */
    }
    if (Date.now() - started > 10 * 60_000) {
      result.value = 'failed'
      return
    }
    poll(started)
  }, 2000)
}
</script>

<template>
  <div class="card upgrade" data-testid="panel-upgrade">
    <div class="row">
      <div class="title">{{ t('settings.upgrade') }}</div>
      <span class="grow" />
      <NButton size="small" :loading="checking" :disabled="!!target && !result" data-testid="upgrade-check" @click="load(true)">
        {{ t('upgrade.check') }}
      </NButton>
    </div>
    <template v-if="st">
      <div class="small">
        {{ t('upgrade.current', { v: st.current }) }}
        <template v-if="latest">
          · {{ t('upgrade.latest', { v: latest.tag }) }}
          <NTag v-if="st.available" size="small" type="warning" :bordered="false" data-testid="upgrade-available">{{ t('upgrade.available') }}</NTag>
          <NTag v-else size="small" type="success" :bordered="false" data-testid="upgrade-uptodate">{{ t('upgrade.upToDate') }}</NTag>
        </template>
        <span class="sd-muted"> · {{ t('upgrade.checkedAt', { time: formatTime(st.checkedAt) }) }}</span>
      </div>
      <div v-if="st.error" class="sd-muted small">{{ st.error }}</div>

      <template v-if="target">
        <NAlert v-if="result === 'done'" type="success" :bordered="false">{{ t('upgrade.done', { v: target }) }}</NAlert>
        <NAlert v-else-if="result === 'failed'" type="error" :bordered="false" data-testid="upgrade-failed">{{ t('upgrade.failed') }}</NAlert>
        <NAlert v-else type="info" :bordered="false" data-testid="upgrade-running">{{ t('upgrade.running', { v: target }) }}</NAlert>
        <pre v-if="log" class="cmd sd-mono log">{{ log }}</pre>
      </template>
      <template v-else-if="st.available && latest">
        <details v-if="latest.notes" class="notes">
          <summary class="small">{{ t('upgrade.notes', { v: latest.tag }) }}</summary>
          <pre class="sd-mono">{{ latest.notes }}</pre>
          <a class="small" :href="latest.url" target="_blank" rel="noopener">{{ latest.url }}</a>
        </details>
        <template v-if="!st.unsupported">
          <div class="row">
            <NInput v-model:value="mirror" size="small" :placeholder="t('upgrade.mirrorPh')" style="max-width: 360px" />
            <NButton size="small" type="primary" data-testid="upgrade-start" @click="upgrade">{{ t('upgrade.confirm', { v: latest.tag }) }}</NButton>
          </div>
          <div class="sd-muted small">{{ t('upgrade.hint') }}</div>
        </template>
      </template>
      <template v-if="st.unsupported">
        <div class="sd-muted small">{{ st.unsupported }}</div>
        <pre class="cmd sd-mono">sshdesktop upgrade</pre>
      </template>
    </template>
  </div>
</template>

<style scoped>
.upgrade {
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.grow {
  flex: 1;
}
.small {
  font-size: 12px;
}
.notes pre {
  white-space: pre-wrap;
  max-height: 200px;
  overflow: auto;
  font-size: 12px;
}
.cmd {
  margin: 0;
  padding: 10px 12px;
  border-radius: 4px;
  background: var(--sd-bg);
  font-size: 12px;
  overflow-x: auto;
}
.log {
  max-height: 220px;
  overflow: auto;
  white-space: pre-wrap;
}
</style>
