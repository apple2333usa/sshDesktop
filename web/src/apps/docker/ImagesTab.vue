<script setup lang="ts">
// 镜像：列表、拉取、删除、清理未使用的镜像。
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCheckbox, NDataTable, NInput, NModal, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { api } from '@/api/http'
import type { DockerImage, Task } from '@/api/types'
import { useSession } from '@/stores/session'
import { useNotify } from '@/stores/notify'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import TaskLog from '@/components/TaskLog.vue'
import { errorText, formatBytes, formatTime } from '@/i18n'
import { shortId } from './util'

const { t } = useI18n()
const message = useMessage()
const session = useSession()
const notify = useNotify()
const hostInfo = useHost()
const canManage = computed(() => session.has('docker:manage'))
const base = '/nodes/local/docker/images'

const list = ref<DockerImage[]>([])
const loading = ref(false)
const q = ref('')
const logTask = ref<Task | null>(null)

async function load() {
  loading.value = true
  try {
    list.value = await api.get<DockerImage[]>(base)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const shown = computed(() => {
  const s = q.value.trim().toLowerCase()
  return s ? list.value.filter((i) => i.tags.some((x) => x.toLowerCase().includes(s)) || i.id.includes(s)) : list.value
})
const total = computed(() => list.value.reduce((n, i) => n + i.size, 0))

const showPull = ref(false)
const pullRef = ref('')
async function pull() {
  try {
    const task = await api.post<Task>(base + '/pull', { image: pullRef.value.trim() })
    showPull.value = false
    logTask.value = task
    const done = await notify.waitTask(task)
    if (done.status === 'success') load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function remove(i: DockerImage) {
  const name = i.tags[0] ?? shortId(i.id)
  try {
    const r = await withDanger(
      {
        level: 1,
        action: t('docker.deleteImage'),
        node: hostInfo.label,
        impact: [name, i.containers.length ? t('docker.imageUsed', { names: i.containers.join('、') }) : t('docker.imageUnused')],
        confirmText: t('docker.deleteNamed', { name }),
      },
      () => api.del(`${base}/${encodeURIComponent(i.id)}`, { query: { force: i.tags.length > 1 ? '1' : undefined } }).then(() => true),
    )
    if (r) load()
  } catch (e) {
    message.error(errorText(e))
  }
}

const pruneAll = ref(false)
const showPrune = ref(false)
async function prune() {
  showPrune.value = false
  try {
    const r = await withDanger(
      { level: 1, action: t('docker.pruneImages'), node: hostInfo.label, impact: pruneAll.value ? t('docker.pruneImagesAll') : t('docker.pruneImagesDangling'), confirmText: t('docker.prune') },
      () => api.post<{ deleted: number; spaceReclaimed: number }>(base + '/prune', { all: pruneAll.value }),
    )
    if (r) {
      message.success(t('docker.pruned', { n: r.deleted, size: formatBytes(r.spaceReclaimed) }))
      load()
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

const columns = computed<DataTableColumns<DockerImage>>(() => [
  {
    title: t('docker.image'),
    key: 'tags',
    minWidth: 220,
    render: (i) =>
      h('div', { class: 'tags' }, i.tags.length ? i.tags.map((x) => h('span', { class: 'sd-mono' }, x)) : [h('span', { class: 'sd-muted sd-mono' }, '<none>')]),
  },
  { title: 'ID', key: 'id', width: 120, render: (i) => h('span', { class: 'sd-mono small' }, shortId(i.id)) },
  { title: t('docker.size'), key: 'size', width: 100, sorter: (a, b) => a.size - b.size, render: (i) => formatBytes(i.size) },
  { title: t('docker.created'), key: 'created', width: 150, sorter: (a, b) => a.created - b.created, render: (i) => formatTime(i.created * 1000) },
  {
    title: t('docker.usedBy'),
    key: 'containers',
    minWidth: 120,
    ellipsis: { tooltip: true },
    render: (i) => (i.containers.length ? i.containers.join(', ') : h(NTag, { size: 'tiny', bordered: false }, () => t('docker.unused'))),
  },
  {
    title: '',
    key: 'actions',
    width: 70,
    render: (i) => (canManage.value ? h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: () => remove(i) }, () => t('common.delete')) : null),
  },
])
</script>

<template>
  <div class="im">
    <div class="sd-toolbar">
      <NInput v-model:value="q" size="small" clearable :placeholder="t('docker.searchImage')" style="max-width: 220px" />
      <span class="sd-muted small">{{ t('docker.imageTotal', { n: list.length, size: formatBytes(total) }) }}</span>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <template v-if="canManage">
        <NButton size="small" @click="(pruneAll = false), (showPrune = true)">{{ t('docker.pruneImages') }}</NButton>
        <NButton size="small" type="primary" data-testid="img-pull" @click="(pullRef = ''), (showPull = true)">{{ t('docker.pull') }}</NButton>
      </template>
    </div>
    <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="columns" :data="shown" :row-key="(i: DockerImage) => i.id" :bordered="false" />

    <NModal v-model:show="showPull" preset="card" :title="t('docker.pull')" style="width: 440px; max-width: calc(100vw - 32px)">
      <NInput v-model:value="pullRef" placeholder="nginx:alpine" data-testid="img-ref" @keyup.enter="pullRef.trim() && pull()" />
      <div class="sd-muted small hint">{{ t('docker.pullHint') }}</div>
      <template #footer>
        <div class="foot">
          <NButton @click="showPull = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :disabled="!pullRef.trim()" data-testid="img-pull-ok" @click="pull">{{ t('docker.pull') }}</NButton>
        </div>
      </template>
    </NModal>
    <NModal v-model:show="showPrune" preset="card" :title="t('docker.pruneImages')" style="width: 440px; max-width: calc(100vw - 32px)">
      <NCheckbox v-model:checked="pruneAll">{{ t('docker.pruneImagesAllOpt') }}</NCheckbox>
      <template #footer>
        <div class="foot">
          <NButton @click="showPrune = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" @click="prune">{{ t('docker.prune') }}</NButton>
        </div>
      </template>
    </NModal>
    <TaskLog v-model:task="logTask" />
  </div>
</template>

<style scoped>
.im {
  height: 100%;
  display: flex;
  flex-direction: column;
}
:deep(.tags) {
  display: flex;
  flex-direction: column;
}
:deep(.small),
.small {
  font-size: 12px;
}
.hint {
  margin-top: 8px;
}
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
</style>
