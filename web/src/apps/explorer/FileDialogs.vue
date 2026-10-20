<script setup lang="ts">
import { computed, nextTick, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCheckbox, NInput, NModal, NRadioButton, NRadioGroup, NSelect, NSpin, NTabPane, NTabs, useMessage,
} from 'naive-ui'
import { compressState, extractState, promptState, propsState } from './dialogs'
import { filesApi } from '@/api/files'
import { errorText, formatBytes, formatTime } from '@/i18n'
import { useSession } from '@/stores/session'
import { fileKind } from '@/components/icons'
import SdIcon from '@/components/SdIcon.vue'

const { t } = useI18n()
const message = useMessage()
const session = useSession()

/* ---------- 输入名称 ---------- */
const promptInput = ref<InstanceType<typeof NInput> | null>(null)
watch(
  () => promptState.show,
  async (s) => {
    if (!s) return
    await nextTick()
    promptInput.value?.focus()
    const el = promptInput.value?.inputElRef as HTMLInputElement | undefined
    if (el) {
      const dot = promptState.value.lastIndexOf('.')
      el.setSelectionRange(0, promptState.selectBase && dot > 0 ? dot : promptState.value.length)
    }
  },
)
function promptDone(ok: boolean) {
  const v = promptState.value.trim()
  if (ok && (!v || v.includes('/'))) {
    message.warning(t('explorer.invalidName'))
    return
  }
  promptState.show = false
  promptState.resolve?.(ok ? v : null)
  promptState.resolve = null
}

/* ---------- 压缩 ---------- */
const comp = reactive({ name: '', format: 'zip' })
watch(
  () => compressState.show,
  (s) => {
    if (!s) return
    const first = compressState.entries[0]
    comp.name = compressState.entries.length === 1 && first ? first.name.replace(/\.[^.]+$/, '') || first.name : t('explorer.archiveName')
    comp.format = 'zip'
  },
)
function compressDone(ok: boolean) {
  compressState.show = false
  const dir = compressState.dir === '/' ? '' : compressState.dir
  compressState.resolve?.(ok && comp.name.trim() ? `${dir}/${comp.name.trim()}.${comp.format}` : null)
  compressState.resolve = null
}

/* ---------- 解压 ---------- */
const ext = reactive({ dest: '', overwrite: false })
watch(
  () => extractState.show,
  (s) => {
    if (!s || !extractState.entry) return
    const e = extractState.entry
    const dir = e.path.slice(0, e.path.lastIndexOf('/')) || '/'
    const base = e.name.replace(/\.(tar\.gz|tar\.xz|tar\.bz2|tgz|txz|tbz2|zip|tar|7z)$/i, '')
    ext.dest = `${dir === '/' ? '' : dir}/${base}`
    ext.overwrite = false
  },
)
function extractDone(ok: boolean) {
  extractState.show = false
  extractState.resolve?.(ok && ext.dest.startsWith('/') ? { dest: ext.dest, overwrite: ext.overwrite } : null)
  extractState.resolve = null
}

/* ---------- 属性 ---------- */
const info = ref<Awaited<ReturnType<typeof filesApi.properties>> | null>(null)
const loadingProps = ref(false)
const perm = reactive({ mode: '0644', owner: '', group: '', recursive: false })
const owners = ref<{ users: { name: string; id: number }[]; groups: { name: string; id: number }[] }>({ users: [], groups: [] })
const saving = ref(false)
const canManage = computed(() => session.has('files:manage'))
const single = computed(() => propsState.entries.length === 1)
const anyDir = computed(() => propsState.entries.some((e) => e.isDir))

watch(
  () => propsState.show,
  async (s) => {
    if (!s) return
    const first = propsState.entries[0]
    info.value = null
    perm.mode = first?.perm ?? '0644'
    perm.owner = first?.owner ?? ''
    perm.group = first?.group ?? ''
    perm.recursive = false
    loadingProps.value = true
    try {
      const [p, o] = await Promise.all([
        single.value ? filesApi.properties(propsState.node, first.path) : Promise.resolve(null),
        canManage.value ? filesApi.owners(propsState.node) : Promise.resolve(owners.value),
      ])
      info.value = p
      owners.value = o
    } catch (e) {
      message.error(errorText(e))
    } finally {
      loadingProps.value = false
    }
  },
)

// 权限位与复选框互相转换
const bits = computed(() => {
  const n = parseInt(perm.mode, 8) || 0
  return [
    [n & 0o400, n & 0o200, n & 0o100],
    [n & 0o40, n & 0o20, n & 0o10],
    [n & 0o4, n & 0o2, n & 0o1],
  ].map((r) => r.map(Boolean))
})
function toggleBit(row: number, col: number) {
  const n = parseInt(perm.mode, 8) || 0
  const bit = [0o400, 0o200, 0o100, 0o40, 0o20, 0o10, 0o4, 0o2, 0o1][row * 3 + col]
  perm.mode = (n ^ bit).toString(8).padStart(4, '0')
}

async function savePerms() {
  const paths = propsState.entries.map((e) => e.path)
  const first = propsState.entries[0]
  saving.value = true
  try {
    if (!/^[0-7]{3,4}$/.test(perm.mode)) throw new Error(t('explorer.badMode'))
    await filesApi.chmod(propsState.node, paths, perm.mode, perm.recursive)
    if (perm.owner !== first.owner || perm.group !== first.group || perm.recursive) {
      await filesApi.chown(propsState.node, paths, perm.owner, perm.group, perm.recursive)
    }
    message.success(t('explorer.permSaved'))
    propsState.onChanged?.()
    propsState.show = false
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}

const userOptions = computed(() => owners.value.users.map((u) => ({ label: `${u.name} (${u.id})`, value: u.name })))
const groupOptions = computed(() => owners.value.groups.map((g) => ({ label: `${g.name} (${g.id})`, value: g.name })))
const totalSize = computed(() => propsState.entries.reduce((s, e) => s + (e.isDir ? 0 : e.size), 0))
</script>

<template>
  <!-- 输入名称 -->
  <NModal
    :show="promptState.show"
    preset="card"
    :title="promptState.title"
    style="width: min(420px, calc(100vw - 32px))"
    :mask-closable="false"
    @update:show="(v: boolean) => !v && promptDone(false)"
  >
    <div v-if="promptState.label" class="lbl">{{ promptState.label }}</div>
    <NInput ref="promptInput" v-model:value="promptState.value" data-testid="prompt-input" @keyup.enter="promptDone(true)" />
    <template #footer>
      <div class="foot">
        <NButton @click="promptDone(false)">{{ t('common.cancel') }}</NButton>
        <NButton type="primary" data-testid="prompt-ok" @click="promptDone(true)">{{ t('common.ok') }}</NButton>
      </div>
    </template>
  </NModal>

  <!-- 压缩 -->
  <NModal
    :show="compressState.show"
    preset="card"
    :title="t('explorer.compress')"
    style="width: min(460px, calc(100vw - 32px))"
    @update:show="(v: boolean) => !v && compressDone(false)"
  >
    <div class="lbl">{{ t('explorer.archiveFileName') }}</div>
    <NInput v-model:value="comp.name" data-testid="compress-name">
      <template #suffix>.{{ comp.format }}</template>
    </NInput>
    <div class="lbl">{{ t('explorer.format') }}</div>
    <NRadioGroup v-model:value="comp.format">
      <NRadioButton value="zip">zip</NRadioButton>
      <NRadioButton value="tar.gz">tar.gz</NRadioButton>
      <NRadioButton value="tar.xz">tar.xz</NRadioButton>
    </NRadioGroup>
    <div class="sd-muted small">{{ t('explorer.compressItems', { n: compressState.entries.length }) }}</div>
    <template #footer>
      <div class="foot">
        <NButton @click="compressDone(false)">{{ t('common.cancel') }}</NButton>
        <NButton type="primary" data-testid="compress-ok" @click="compressDone(true)">{{ t('explorer.compress') }}</NButton>
      </div>
    </template>
  </NModal>

  <!-- 解压 -->
  <NModal
    :show="extractState.show"
    preset="card"
    :title="t('explorer.extractTitle', { name: extractState.entry?.name ?? '' })"
    style="width: min(500px, calc(100vw - 32px))"
    @update:show="(v: boolean) => !v && extractDone(false)"
  >
    <div class="lbl">{{ t('explorer.extractTo') }}</div>
    <NInput v-model:value="ext.dest" data-testid="extract-dest" />
    <div class="row-gap"><NCheckbox v-model:checked="ext.overwrite">{{ t('explorer.overwriteExisting') }}</NCheckbox></div>
    <template #footer>
      <div class="foot">
        <NButton @click="extractDone(false)">{{ t('common.cancel') }}</NButton>
        <NButton type="primary" data-testid="extract-ok" @click="extractDone(true)">{{ t('explorer.extract') }}</NButton>
      </div>
    </template>
  </NModal>

  <!-- 属性 -->
  <NModal
    v-model:show="propsState.show"
    preset="card"
    :title="single ? t('explorer.propertiesOf', { name: propsState.entries[0]?.name }) : t('explorer.properties')"
    style="width: min(520px, calc(100vw - 32px))"
  >
    <NSpin :show="loadingProps">
      <NTabs type="line" animated>
        <NTabPane name="general" :tab="t('explorer.general')">
          <div class="head">
            <SdIcon :name="single ? fileKind(propsState.entries[0]?.name ?? '', !!propsState.entries[0]?.isDir) : 'file'" :size="40" />
            <div class="name">{{ single ? propsState.entries[0]?.name : t('explorer.nItems', { n: propsState.entries.length }) }}</div>
          </div>
          <table class="kv">
            <tbody>
              <template v-if="single && propsState.entries[0]">
                <tr><td>{{ t('explorer.location') }}</td><td class="sd-mono">{{ propsState.entries[0].path }}</td></tr>
                <tr><td>{{ t('explorer.type') }}</td><td>{{ t('explorer.types.' + propsState.entries[0].type) }}</td></tr>
                <tr v-if="propsState.entries[0].linkTarget"><td>{{ t('explorer.linkTarget') }}</td><td class="sd-mono">{{ propsState.entries[0].linkTarget }}</td></tr>
                <tr>
                  <td>{{ t('explorer.size') }}</td>
                  <td>
                    <template v-if="propsState.entries[0].isDir && info">
                      {{ formatBytes(info.size ?? 0) }}（{{ t('explorer.dirStats', { files: info.files, dirs: info.dirs }) }}）
                      <span v-if="info.partial" class="sd-muted">{{ t('explorer.partial') }}</span>
                    </template>
                    <template v-else>{{ formatBytes(propsState.entries[0].size) }}（{{ propsState.entries[0].size.toLocaleString() }} B）</template>
                  </td>
                </tr>
                <tr><td>{{ t('explorer.modified') }}</td><td>{{ formatTime(propsState.entries[0].mtime, true) }}</td></tr>
                <tr v-if="info?.atime"><td>{{ t('explorer.accessed') }}</td><td>{{ formatTime(info.atime, true) }}</td></tr>
                <tr v-if="info?.ctime"><td>{{ t('explorer.changed') }}</td><td>{{ formatTime(info.ctime, true) }}</td></tr>
              </template>
              <template v-else>
                <tr><td>{{ t('explorer.size') }}</td><td>{{ formatBytes(totalSize) }}{{ anyDir ? t('explorer.excludingDirs') : '' }}</td></tr>
              </template>
            </tbody>
          </table>
        </NTabPane>
        <NTabPane name="perm" :tab="t('explorer.permissions')">
          <table class="bits">
            <thead>
              <tr><th /><th>{{ t('explorer.read') }}</th><th>{{ t('explorer.write') }}</th><th>{{ t('explorer.exec') }}</th></tr>
            </thead>
            <tbody>
              <tr v-for="(row, i) in bits" :key="i">
                <td>{{ [t('explorer.owner'), t('explorer.group'), t('explorer.others')][i] }}</td>
                <td v-for="(on, j) in row" :key="j">
                  <NCheckbox :checked="on" :disabled="!canManage" @update:checked="toggleBit(i, j)" />
                </td>
              </tr>
            </tbody>
          </table>
          <div class="grid2">
            <span>{{ t('explorer.modeOctal') }}</span>
            <NInput v-model:value="perm.mode" :disabled="!canManage" class="sd-mono" data-testid="perm-mode" />
            <span>{{ t('explorer.owner') }}</span>
            <NSelect v-model:value="perm.owner" :options="userOptions" filterable tag :disabled="!canManage" />
            <span>{{ t('explorer.group') }}</span>
            <NSelect v-model:value="perm.group" :options="groupOptions" filterable tag :disabled="!canManage" />
          </div>
          <NCheckbox v-if="anyDir" v-model:checked="perm.recursive" :disabled="!canManage">{{ t('explorer.applyRecursive') }}</NCheckbox>
          <div class="foot top">
            <NButton type="primary" :disabled="!canManage" :loading="saving" data-testid="perm-save" @click="savePerms">{{ t('common.apply') }}</NButton>
          </div>
        </NTabPane>
      </NTabs>
    </NSpin>
  </NModal>
</template>

<style scoped>
.lbl {
  margin: 10px 0 6px;
  color: var(--sd-text-2);
}
.lbl:first-child {
  margin-top: 0;
}
.foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
.foot.top {
  margin-top: 12px;
}
.small {
  font-size: 12px;
  margin-top: 10px;
}
.row-gap {
  margin-top: 10px;
}
.head {
  display: flex;
  align-items: center;
  gap: 12px;
  margin: 8px 0 12px;
}
.name {
  font-weight: 600;
  word-break: break-all;
}
.kv td {
  padding: 4px 12px 4px 0;
  vertical-align: top;
  word-break: break-all;
}
.kv td:first-child {
  color: var(--sd-text-2);
  white-space: nowrap;
}
.bits {
  border-collapse: collapse;
  margin-bottom: 12px;
}
.bits th,
.bits td {
  padding: 4px 14px 4px 0;
  text-align: center;
  font-weight: normal;
}
.bits td:first-child {
  text-align: left;
}
.grid2 {
  display: grid;
  grid-template-columns: 90px 1fr;
  gap: 8px;
  align-items: center;
  margin-bottom: 10px;
}
</style>
