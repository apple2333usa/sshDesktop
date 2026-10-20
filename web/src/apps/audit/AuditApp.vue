<script setup lang="ts">
// 操作审计：按用户、模块、结果、时间筛选，分页，导出 CSV。审计日志只能查询和导出，不能删除。
import { computed, h, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDataTable, NDatePicker, NInput, NSelect, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { api, buildQuery } from '@/api/http'
import type { AuditRow } from '@/api/types'
import { triggerDownload } from '@/api/files'
import { useAppHost } from '@/apps/host'
import { errorText, formatTime } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
host.setTitle(t('apps.audit'))

const items = ref<AuditRow[]>([])
const modules = ref<string[]>([])
const total = ref(0)
const loading = ref(false)
const f = reactive({ user: '', module: null as string | null, result: null as string | null, range: null as [number, number] | null, q: '' })
const pager = reactive({ page: 1, pageSize: 50 })

function query() {
  return {
    user: f.user.trim() || undefined,
    module: f.module ?? undefined,
    result: f.result ?? undefined,
    from: f.range?.[0],
    to: f.range?.[1],
    q: f.q.trim() || undefined,
  }
}

async function load() {
  loading.value = true
  try {
    const r = await api.get<{ total: number; items: AuditRow[]; modules: string[] }>('/audit', { ...query(), page: pager.page, size: pager.pageSize })
    items.value = r.items
    total.value = r.total
    modules.value = r.modules ?? []
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

function search() {
  pager.page = 1
  load()
}

function exportCSV() {
  triggerDownload('/api/v1/audit/export' + buildQuery(query()))
}

const moduleLabel = (m: string) => {
  const k = 'audit.modules.' + m
  const v = t(k)
  return v === k ? m : v
}

const moduleOptions = computed(() => modules.value.map((m) => ({ label: moduleLabel(m), value: m })))
const resultOptions = computed(() => [
  { label: t('audit.success'), value: 'ok' },
  { label: t('audit.failure'), value: 'fail' },
])

const columns = computed<DataTableColumns<AuditRow>>(() => [
  { title: t('audit.time'), key: 'at', width: 160, render: (r) => formatTime(r.at, true) },
  { title: t('audit.user'), key: 'username', width: 100, render: (r) => r.username || '-' },
  { title: 'IP', key: 'ip', width: 130 },
  { title: t('audit.module'), key: 'module', width: 100, render: (r) => moduleLabel(r.module) },
  { title: t('audit.action'), key: 'action', width: 130 },
  { title: t('audit.target'), key: 'target', ellipsis: { tooltip: true }, minWidth: 200 },
  {
    title: t('audit.result'),
    key: 'success',
    width: 80,
    render: (r) => h(NTag, { size: 'small', bordered: false, type: r.success ? 'success' : 'error' }, () => (r.success ? t('audit.success') : t('audit.failure'))),
  },
  { title: t('audit.detail'), key: 'detail', ellipsis: { tooltip: true }, minWidth: 160, render: (r) => r.detail || '' },
  { title: t('audit.duration'), key: 'durationMs', width: 80, render: (r) => (r.durationMs ? `${r.durationMs} ms` : '') },
])

const pagination = computed(() => ({
  page: pager.page,
  pageSize: pager.pageSize,
  itemCount: total.value,
  showSizePicker: true,
  pageSizes: [50, 100, 200],
  prefix: () => t('audit.total', { n: total.value }),
  onUpdatePage: (p: number) => {
    pager.page = p
    load()
  },
  onUpdatePageSize: (s: number) => {
    pager.pageSize = s
    pager.page = 1
    load()
  },
}))
</script>

<template>
  <div class="sd-page audit" data-testid="audit">
    <div class="sd-toolbar filters">
      <NInput v-model:value="f.user" size="small" clearable :placeholder="t('audit.user')" style="width: 120px" @keydown.enter="search" />
      <NSelect v-model:value="f.module" size="small" clearable :options="moduleOptions" :placeholder="t('audit.module')" style="width: 130px" @update:value="search" />
      <NSelect v-model:value="f.result" size="small" clearable :options="resultOptions" :placeholder="t('audit.result')" style="width: 100px" @update:value="search" />
      <NDatePicker v-model:value="f.range" type="datetimerange" size="small" clearable style="width: 340px" @update:value="search" />
      <NInput v-model:value="f.q" size="small" clearable :placeholder="t('audit.keyword')" style="width: 180px" @keydown.enter="search" />
      <NButton size="small" type="primary" @click="search">{{ t('audit.search') }}</NButton>
      <NButton size="small" @click="exportCSV">{{ t('audit.export') }}</NButton>
    </div>
    <NDataTable
      class="sd-fill"
      size="small"
      flex-height
      remote
      :loading="loading"
      :columns="columns"
      :data="items"
      :row-key="(r: AuditRow) => r.id"
      :pagination="pagination"
      :bordered="false"
      :scroll-x="1200"
    />
  </div>
</template>

<style scoped>
.audit {
  background: var(--sd-surface);
  padding-bottom: 8px;
}
.filters {
  flex-wrap: wrap;
}
</style>
