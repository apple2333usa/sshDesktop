<script setup lang="ts">
// 网络：列表、新建、删除、清理未使用的网络。
import { computed, h, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NCheckbox, NDataTable, NForm, NFormItem, NInput, NModal, NSelect, NTag, useMessage, type DataTableColumns } from 'naive-ui'
import { api } from '@/api/http'
import type { DockerNetwork } from '@/api/types'
import { useSession } from '@/stores/session'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import { errorText } from '@/i18n'

const { t } = useI18n()
const message = useMessage()
const session = useSession()
const hostInfo = useHost()
const canManage = computed(() => session.has('docker:manage'))
const base = '/nodes/local/docker/networks'

const list = ref<DockerNetwork[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    list.value = await api.get<DockerNetwork[]>(base)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const showCreate = ref(false)
const form = ref({ name: '', driver: 'bridge', subnet: '', gateway: '', internal: false })
async function create() {
  try {
    await api.post(base, form.value)
    showCreate.value = false
    load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function remove(n: DockerNetwork) {
  try {
    const r = await withDanger(
      { level: 1, action: t('docker.deleteNetwork'), node: hostInfo.label, impact: n.name, confirmText: t('docker.deleteNamed', { name: n.name }) },
      () => api.del(`${base}/${n.id}`).then(() => true),
    )
    if (r) load()
  } catch (e) {
    message.error(errorText(e))
  }
}

async function prune() {
  try {
    const r = await withDanger(
      { level: 1, action: t('docker.pruneNetworks'), node: hostInfo.label, impact: t('docker.pruneNetworksImpact'), confirmText: t('docker.prune') },
      () => api.post<{ deleted: number }>(base + '/prune'),
    )
    if (r) {
      message.success(t('docker.prunedN', { n: r.deleted }))
      load()
    }
  } catch (e) {
    message.error(errorText(e))
  }
}

const columns = computed<DataTableColumns<DockerNetwork>>(() => [
  {
    title: t('docker.name'),
    key: 'name',
    minWidth: 160,
    render: (n) => h('div', { class: 'cname' }, [h('span', { class: 'sd-mono' }, n.name), n.builtin ? h(NTag, { size: 'tiny', bordered: false }, () => t('docker.builtin')) : null]),
  },
  { title: t('docker.driver'), key: 'driver', width: 90 },
  { title: t('docker.subnet'), key: 'subnet', width: 160, render: (n) => h('span', { class: 'sd-mono small' }, n.subnet ? `${n.subnet}${n.gateway ? ' / ' + n.gateway : ''}` : '-') },
  { title: t('docker.usedBy'), key: 'containers', minWidth: 140, ellipsis: { tooltip: true }, render: (n) => n.containers.join(', ') || '-' },
  {
    title: '',
    key: 'actions',
    width: 70,
    render: (n) => (canManage.value && !n.builtin ? h(NButton, { size: 'tiny', quaternary: true, type: 'error', onClick: () => remove(n) }, () => t('common.delete')) : null),
  },
])
</script>

<template>
  <div class="nw">
    <div class="sd-toolbar">
      <b>{{ t('docker.networkCount', { n: list.length }) }}</b>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
      <template v-if="canManage">
        <NButton size="small" @click="prune">{{ t('docker.pruneNetworks') }}</NButton>
        <NButton size="small" type="primary" @click="(form = { name: '', driver: 'bridge', subnet: '', gateway: '', internal: false }), (showCreate = true)">{{ t('docker.createNetwork') }}</NButton>
      </template>
    </div>
    <NDataTable class="sd-fill" size="small" flex-height :loading="loading" :columns="columns" :data="list" :row-key="(n: DockerNetwork) => n.id" :bordered="false" />
    <NModal v-model:show="showCreate" preset="card" :title="t('docker.createNetwork')" style="width: 460px; max-width: calc(100vw - 32px)">
      <NForm label-placement="left" label-width="80" size="small">
        <NFormItem :label="t('docker.name')" required>
          <NInput v-model:value="form.name" />
        </NFormItem>
        <NFormItem :label="t('docker.driver')">
          <NSelect v-model:value="form.driver" :options="['bridge', 'macvlan', 'ipvlan', 'overlay'].map((v) => ({ label: v, value: v }))" />
        </NFormItem>
        <NFormItem :label="t('docker.subnet')">
          <NInput v-model:value="form.subnet" :placeholder="t('docker.subnetPh')" />
        </NFormItem>
        <NFormItem :label="t('docker.gateway')">
          <NInput v-model:value="form.gateway" :disabled="!form.subnet" placeholder="172.30.0.1" />
        </NFormItem>
        <NFormItem label=" ">
          <NCheckbox v-model:checked="form.internal">{{ t('docker.internal') }}</NCheckbox>
        </NFormItem>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="showCreate = false">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :disabled="!form.name.trim()" @click="create">{{ t('common.ok') }}</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.nw {
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
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
</style>
