<script setup lang="ts">
// 存储卷：列表、新建、删除和清理（会删除数据，需要输入密码确认）。
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NDataTable, NInput, NModal, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { api } from '@/api/http'
import type { DockerVolume } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { useSession } from '@/stores/session'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import { errorText, formatTime } from '@/i18n'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const session = useSession()
const hostInfo = useHost()
const canManage = computed(() => session.has('docker:manage'))
const base = '/nodes/local/docker/volumes'

const list = ref<DockerVolume[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    list.value = await api.get<DockerVolume[]>(base)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const showCreate = ref(false)
const name = ref('')
async function create() {
  try {
    await api.post(base, { name: name.value.trim() })
    showCreate.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function remove(v: DockerVolume) {
  try {
    const r = await withDanger(
      {
        level: 2,
        action: t('docker.deleteVolume'),
        node: hostInfo.label,
        impact: [v.name, v.mountpoint, t('docker.deleteVolumeImpact')],
        confirmText: t('docker.deleteNamed', { name: v.name }),
      },
      (pw) => api.del(`${base}/${encodeURIComponent(v.name)}`, { confirmPassword: pw }).then(() => true),
    )
    if (r) load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function prune() {
  const unused = list.value.filter((v) => !v.containers.length)
  try {
    const r = await withDanger(
      {
        level: 2,
        action: t('docker.pruneVolumes'),
        node: hostInfo.label,
        impact: [t('docker.pruneVolumesImpact', { n: unused.length }), ...unused.slice(0, 10).map((v) => v.name)],
        confirmText: t('docker.prune'),
      },
      (pw) => api.post<{ deleted: number }>(base + '/prune', {}, { confirmPassword: pw }),
    )
    if (r) {
      message.success(t('docker.prunedN', { n: r.deleted }))
      load()
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

const columns = computed<DataTableColumns<DockerVolume>>(() => [
  {
    title: t('docker.name'),
    key: 'name',
    minWidth: 200,
    ellipsis: { tooltip: true },
    render: (v) => h('div', { class: 'cname' }, [h('span', { class: 'sd-mono' }, v.name), v.project ? h(NTag, { size: 'tiny', bordered: false }, () => v.project) : null]),
  },
  { title: t('docker.driver'), key: 'driver', width: 80 },
  {
    title: t('docker.mountpoint'),
    key: 'mountpoint',
    minWidth: 200,
    ellipsis: { tooltip: true },
    render: (v) =>
      session.has('files:view')
        ? h('a', { class: 'sd-mono small link', onClick: () => host.openApp('explorer', { node: 'local', path: v.mountpoint }) }, v.mountpoint)
        : h('span', { class: 'sd-mono small' }, v.mountpoint),
  },
  { title: t('docker.usedBy'), key: 'containers', minWidth: 120, ellipsis: { tooltip: true }, render: (v) => v.containers.join(', ') || h(NTag, { size: 'tiny', bordered: false }, () => t('docker.unused')) },
  { title: t('docker.created'), key: 'created', width: 150, render: (v) => (v.created ? formatTime(v.created) : '-') },
  {
    title: '',
    key: 'actions',
    width: 70,
    render: (v) => (canManage.value ? h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: () => remove(v) }, () => t('common.delete')) : null),
  },
])
</script>

<template>
  <div class="vol">
    <div class="sd-toolbar">
      <b>{{ t('docker.volumeCount', { n: list.length }) }}</b>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <template v-if="canManage">
        <NButton size="small" @click="prune">{{ t('docker.pruneVolumes') }}</NButton>
        <NButton size="small" type="primary" @click="(name = ''), (showCreate = true)">{{ t('docker.createVolume') }}</NButton>
      </template>
    </div>
    <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="columns" :data="list" :row-key="(v: DockerVolume) => v.name" :bordered="false" />
    <NModal v-model:show="showCreate" preset="card" :title="t('docker.createVolume')" style="width: 400px; max-width: calc(100vw - 32px)">
      <NInput v-model:value="name" :placeholder="t('docker.name')" @keyup.enter="name.trim() && create()" />
      <template #footer>
        <div class="foot">
          <NButton @click="showCreate = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :disabled="!name.trim()" @click="create">{{ t('common.ok') }}</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.vol {
  height: 100%;
  display: flex;
  flex-direction: column;
}
:deep(.cname) {
  display: flex;
  gap: 6px;
  align-items: center;
}
:deep(.small) {
  font-size: 12px;
}
:deep(.link) {
  cursor: pointer;
  color: var(--sd-accent);
}
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
</style>
