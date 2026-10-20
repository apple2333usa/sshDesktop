<script setup lang="ts">
// 回收站：按挂载点保存，记录原路径，可还原；永久删除和清空回收站是二级危险操作。
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDataTable, useMessage, type DataTableColumns } from 'naive-ui'
import { filesApi } from '@/api/files'
import type { TrashItem } from '@/api/types'
import { withDanger } from '@/components/danger'
import { errorText, formatBytes, formatTime } from '@/i18n'
import { useSession } from '@/stores/session'
import { fileKind } from '@/components/icons'
import SdIcon from '@/components/SdIcon.vue'

const props = defineProps<{ node: string }>()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const items = ref<TrashItem[]>([])
const checked = ref<string[]>([])
const loading = ref(false)
const canManage = computed(() => session.has('files:manage'))

const key = (i: TrashItem) => `${i.root}|${i.id}`

async function load() {
  loading.value = true
  try {
    items.value = await filesApi.trash(props.node)
    checked.value = checked.value.filter((k) => items.value.some((i) => key(i) === k))
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const selectedItems = computed(() => items.value.filter((i) => checked.value.includes(key(i))))

async function restore() {
  try {
    const r = await filesApi.trashRestore(props.node, selectedItems.value.map((i) => ({ root: i.root, id: i.id })))
    message.success(t('explorer.restored', { n: r.paths.length }))
  } catch (e) {
    message.error(errorText(e))
  }
  load()
}

async function purge() {
  const list = selectedItems.value
  try {
    await withDanger(
      { level: 2, action: t('explorer.permanentDeleteAction'), impact: list.map((i) => i.original), confirmText: t('explorer.deleteN', { n: list.length }) },
      (pw) => filesApi.trashDelete(props.node, list.map((i) => ({ root: i.root, id: i.id })), pw!),
    )
  } catch (e) {
    message.error(errorText(e))
  }
  load()
}

async function empty() {
  try {
    await withDanger(
      { level: 2, action: t('explorer.emptyTrashAction'), impact: t('explorer.emptyTrashImpact', { n: items.value.length }), confirmText: t('explorer.emptyTrash') },
      (pw) => filesApi.trashEmpty(props.node, pw!),
    )
  } catch (e) {
    message.error(errorText(e))
  }
  load()
}

const columns = computed<DataTableColumns<TrashItem>>(() => [
  { type: 'selection' },
  {
    title: t('explorer.name'),
    key: 'name',
    ellipsis: { tooltip: true },
    render: (r) => h('span', { style: 'display:flex;gap:8px;align-items:center' }, [h(SdIcon, { name: fileKind(r.name, r.isDir), size: 18 }), r.name]),
  },
  { title: t('explorer.originalLocation'), key: 'original', ellipsis: { tooltip: true } },
  { title: t('explorer.deletedAt'), key: 'deletedAt', width: 150, render: (r) => formatTime(r.deletedAt) },
  { title: t('explorer.size'), key: 'size', width: 90, render: (r) => (r.isDir ? '' : formatBytes(r.size)) },
])

defineExpose({ reload: load })
</script>

<template>
  <div class="sd-page">
    <div class="sd-toolbar">
      <NButton size="small" :disabled="!canManage || !checked.length" data-testid="trash-restore" @click="restore">{{ t('explorer.restore') }}</NButton>
      <NButton size="small" :disabled="!canManage || !checked.length" @click="purge">{{ t('explorer.deletePermanently') }}</NButton>
      <NButton size="small" :disabled="!canManage || !items.length" data-testid="trash-empty" @click="empty">{{ t('explorer.emptyTrash') }}</NButton>
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <span class="sd-muted hint">{{ t('explorer.trashHint') }}</span>
    </div>
    <NDataTable
      class="sd-fill"
      flex-height
      size="small"
      :loading="loading"
      :columns="columns"
      :data="items"
      :row-key="key"
      :checked-row-keys="checked"
      :bordered="false"
      @update:checked-row-keys="(k) => (checked = k as string[])"
    />
  </div>
</template>

<style scoped>
.hint {
  font-size: 12px;
  margin-left: auto;
}
</style>
