<script setup lang="ts">
// 已有站点：宿主机 Nginx 中不由面板管理的站点配置。结构简单的可以一键接管为面板站点（原文件备份后移除，可以还原），
// 其他的保持原样，可以直接编辑原文件（保存前用 nginx -t 校验，失败自动恢复）。
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NEmpty, NInput, NModal, NTag, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { ExistingFile } from '@/api/types'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import { errorText } from '@/i18n'

const props = defineProps<{ canManage: boolean }>()
const emit = defineEmits<{ changed: [] }>()
const { t } = useI18n()
const message = useMessage()
const hostInfo = useHost()
const base = '/nodes/local/websites'

const files = ref<ExistingFile[]>([])
const loading = ref(false)
async function load() {
  loading.value = true
  try {
    files.value = await api.get<ExistingFile[]>(base + '/existing')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function takeover(f: ExistingFile) {
  const w = f.takeover!
  try {
    const ok = await withDanger(
      {
        level: 1,
        action: t('websites.takeoverSite'),
        node: hostInfo.label,
        impact: [f.path, `${w.domains} → ${w.type === 'proxy' ? w.proxyPass : w.root}${w.https ? ' (HTTPS)' : ''}`, t('websites.takeoverImpact')],
        confirmText: t('websites.takeoverSite'),
      },
      () => api.post(base + '/existing/takeover', { path: f.path }).then(() => true),
    )
    if (ok) {
      message.success(t('websites.takenOverOk'))
      load()
      emit('changed')
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

/* ---------- 编辑原文件 ---------- */
const editFile = ref<{ path: string; content: string; hash: string; readonly: boolean } | null>(null)
async function openFile(path: string) {
  try {
    const r = await api.get<{ path: string; content: string; hash: string }>(base + '/existing/file', { path })
    editFile.value = { ...r, readonly: !props.canManage }
  } catch (e) {
    message.error(errorText(e))
  }
}
async function openMain() {
  try {
    const r = await api.get<{ path: string; content: string }>(base + '/runtime/main-conf')
    editFile.value = { ...r, hash: '', readonly: true }
  } catch (e) {
    message.error(errorText(e))
  }
}
const saving = ref(false)
async function saveFile() {
  if (!editFile.value) return
  saving.value = true
  try {
    await api.put(base + '/existing/file', { path: editFile.value.path, content: editFile.value.content, hash: editFile.value.hash })
    message.success(t('common.saved'))
    editFile.value = null
    load()
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="existing">
    <div class="sd-toolbar">
      <span class="sd-muted small">{{ t('websites.existingHint') }}</span>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="openMain">{{ t('websites.mainConf') }}</NButton>
      <NButton size="small" quaternary :loading="loading" @click="load">{{ t('explorer.refresh') }}</NButton>
    </div>
    <div class="list sd-scroll">
      <NEmpty v-if="!loading && !files.length" :description="t('websites.noExisting')" />
      <div v-for="f in files" :key="f.path" class="file" :data-testid="'existing-' + f.path">
        <div class="head">
          <b class="sd-mono">{{ f.path }}</b>
          <span style="flex: 1" />
          <NButton size="tiny" quaternary @click="openFile(f.path)">{{ canManage ? t('common.edit') : t('websites.view') }}</NButton>
          <NButton v-if="canManage && f.takeover" size="tiny" type="primary" data-testid="existing-takeover" @click="takeover(f)">{{ t('websites.takeoverSite') }}</NButton>
        </div>
        <NAlert v-if="f.error" type="error" :bordered="false">{{ f.error }}</NAlert>
        <div v-for="s in f.servers" :key="s.line" class="server small">
          <span class="sd-mono">{{ s.names.join(' ') || '_' }}</span>
          <span class="sd-muted">{{ s.listen.join(', ') }}</span>
          <NTag v-if="s.ssl" size="tiny" type="success" :bordered="false">HTTPS</NTag>
          <span class="sd-muted sd-mono">{{ s.proxyPass || s.root || s.redirect || '' }}</span>
          <span v-if="s.reasons.length" class="sd-muted">· {{ s.reasons.join('、') }}</span>
        </div>
        <div v-if="!f.takeover && f.reason" class="sd-muted small">{{ f.reason }}</div>
      </div>
    </div>
    <NModal :show="!!editFile" preset="card" :title="editFile?.path" style="width: 820px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && (editFile = null)">
      <NInput v-if="editFile" v-model:value="editFile.content" type="textarea" class="code" :readonly="editFile.readonly" :autosize="{ minRows: 16, maxRows: 30 }" />
      <div v-if="editFile && !editFile.readonly" class="sd-muted small" style="margin-top: 6px">{{ t('websites.rawSaveHint') }}</div>
      <template #footer>
        <div class="foot">
          <NButton @click="editFile = null">{{ t('common.close') }}</NButton>
          <NButton v-if="editFile && !editFile.readonly" type="primary" :loading="saving" @click="saveFile">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.existing {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
.small {
  font-size: 12px;
}
.list {
  flex: 1;
  min-height: 0;
  overflow: auto;
  padding: 0 12px 12px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.file {
  border: 1px solid var(--sd-border);
  border-radius: 6px;
  padding: 8px 10px;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.head,
.server {
  display: flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
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
</style>
