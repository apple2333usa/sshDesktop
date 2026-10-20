<script setup lang="ts">
// 安装应用或修改已安装应用的参数：表单字段来自应用包的 formFields。
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NForm, NFormItem, NInput, NInputNumber, NModal, NSelect, NSpin, NSwitch, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { InstalledApp, StoreApp, StoreField, Task } from '@/api/types'
import { errorText } from '@/i18n'

const props = defineProps<{
  /** 安装：应用；修改参数：已安装应用名 */
  app: StoreApp | null
  installed: string | null
}>()
const emit = defineEmits<{ close: []; task: [Task] }>()
const { t } = useI18n()
const message = useMessage()

const loading = ref(false)
const saving = ref(false)
const fields = ref<StoreField[]>([])
const values = ref<Record<string, string>>({})
const name = ref('')
const version = ref('')
const external = ref(true)
const title = ref('')
const versions = ref<string[]>([])
const unsupported = ref('')
const show = computed(() => !!props.app || !!props.installed)
const editMode = computed(() => !!props.installed)

async function loadFields(ver?: string) {
  if (!props.app) return
  loading.value = true
  try {
    const r = await api.get<{ fields: StoreField[]; version: string; suggestName: string; app: StoreApp }>(
      `/nodes/local/appstore/apps/${props.app.source}/${props.app.key}`,
      { version: ver },
    )
    fields.value = r.fields
    values.value = Object.fromEntries(r.fields.map((f) => [f.envKey, f.value]))
    version.value = r.version
    if (!name.value) name.value = r.suggestName
    unsupported.value = r.app.unsupported ?? ''
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}

watch(
  () => [props.app, props.installed] as const,
  async ([app, inst]) => {
    fields.value = []
    values.value = {}
    name.value = ''
    unsupported.value = ''
    if (app) {
      title.value = t('appstore.installTitle', { name: app.name })
      versions.value = app.versions
      external.value = true
      await loadFields()
    } else if (inst) {
      loading.value = true
      try {
        const r = await api.get<{ app: InstalledApp; fields: StoreField[] }>(`/nodes/local/appstore/installed/${inst}`)
        title.value = t('appstore.paramsTitle', { name: r.app.title + '（' + r.app.name + '）' })
        fields.value = r.fields
        values.value = Object.fromEntries(r.fields.map((f) => [f.envKey, f.value]))
        external.value = r.app.external
      } catch (e) {
        message.error(errorText(e))
      } finally {
        loading.value = false
      }
    }
  },
  { immediate: true },
)

// 修改参数时只显示可编辑的字段
const shown = computed(() => (editMode.value ? fields.value.filter((f) => f.edit) : fields.value).filter((f) => f.type !== 'apps' && f.type !== 'service'))
const hasPort = computed(() => fields.value.some((f) => f.rule === 'paramPort' || (f.type === 'number' && f.envKey.includes('PORT'))))
const missing = computed(() => shown.value.some((f) => f.required && !String(values.value[f.envKey] ?? '').trim()))

function options(f: StoreField) {
  return (f.values ?? []).map((o) => ({ label: o.label || String(o.value), value: String(o.value) }))
}

async function submit() {
  saving.value = true
  try {
    let task: Task
    if (props.installed) {
      const changed: Record<string, string> = {}
      for (const f of shown.value) changed[f.envKey] = values.value[f.envKey] ?? ''
      task = await api.put<Task>(`/nodes/local/appstore/installed/${props.installed}/params`, { params: changed, external: external.value })
    } else {
      task = await api.post<Task>('/nodes/local/appstore/installed', {
        source: props.app!.source,
        key: props.app!.key,
        version: version.value,
        name: name.value.trim(),
        params: values.value,
        external: external.value,
      })
    }
    emit('task', task)
    emit('close')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <NModal :show="show" preset="card" :title="title" style="width: 600px; max-width: calc(100vw - 32px)" data-testid="app-install" @update:show="(v: boolean) => !v && emit('close')">
    <NSpin :show="loading">
      <NAlert v-if="unsupported" type="warning" :bordered="false" class="mb">{{ unsupported }}</NAlert>
      <NForm label-placement="left" label-width="120" size="small" class="form sd-scroll">
        <template v-if="!editMode">
          <NFormItem :label="t('appstore.name')" required>
            <NInput v-model:value="name" data-testid="app-name" />
          </NFormItem>
          <NFormItem :label="t('appstore.version')">
            <NSelect v-model:value="version" :options="versions.map((v) => ({ label: v, value: v }))" style="width: 200px" @update:value="(v: string) => loadFields(v)" />
          </NFormItem>
        </template>
        <NFormItem v-for="f in shown" :key="f.envKey" :label="f.label" :required="f.required">
          <div class="field">
            <NSelect
              v-if="f.type === 'select'"
              :value="f.multiple ? (values[f.envKey] ? values[f.envKey].split(',') : []) : values[f.envKey]"
              :multiple="f.multiple"
              :options="options(f)"
              :disabled="f.disabled"
              @update:value="(v: string | string[]) => (values[f.envKey] = Array.isArray(v) ? v.join(',') : v)"
            />
            <NInputNumber
              v-else-if="f.type === 'number'"
              :value="values[f.envKey] === '' || values[f.envKey] === undefined ? null : Number(values[f.envKey])"
              :disabled="f.disabled"
              :show-button="false"
              style="width: 200px"
              :data-testid="'field-' + f.envKey"
              @update:value="(v: number | null) => (values[f.envKey] = v === null ? '' : String(v))"
            />
            <NInput
              v-else
              v-model:value="values[f.envKey]"
              :type="f.type === 'password' ? 'password' : 'text'"
              show-password-on="click"
              :disabled="f.disabled"
              :data-testid="'field-' + f.envKey"
            />
            <div v-if="f.hint" class="sd-muted hint">{{ f.hint }}</div>
          </div>
        </NFormItem>
        <NFormItem v-if="hasPort" :label="t('appstore.external')">
          <div class="field">
            <NSwitch v-model:value="external" data-testid="app-external" />
            <div class="sd-muted hint">{{ external ? t('appstore.externalOn') : t('appstore.externalOff') }}</div>
          </div>
        </NFormItem>
      </NForm>
      <div v-if="editMode" class="sd-muted hint">{{ t('appstore.paramsHint') }}</div>
    </NSpin>
    <template #footer>
      <div class="foot">
        <NButton @click="emit('close')">{{ t('common.cancel') }}</NButton>
        <NButton type="primary" :loading="saving" :disabled="loading || missing || !!unsupported || (!editMode && !name.trim())" data-testid="app-install-ok" @click="submit">
          {{ editMode ? t('appstore.saveParams') : t('appstore.install') }}
        </NButton>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.mb {
  margin-bottom: 10px;
}
.form {
  max-height: 60vh;
  padding-right: 6px;
}
.field {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.hint {
  font-size: 12px;
}
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
</style>
