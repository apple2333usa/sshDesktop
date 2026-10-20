<script setup lang="ts">
// 添加 / 修改服务器：填写地址 → 获取并核对主机指纹 → 填写凭据 → 测试连接并保存。
// 修改时凭据留空表示不变；地址或端口变了要重新确认指纹。
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCheckbox, NColorPicker, NForm, NFormItem, NInput, NInputNumber, NModal, NRadioButton, NRadioGroup, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { HostKeyScan, NodeForm, NodeView } from '@/api/types'
import { errorText } from '@/i18n'

const props = defineProps<{ show: boolean; edit: NodeView | null }>()
const emit = defineEmits<{ 'update:show': [boolean]; saved: [NodeView] }>()
const { t } = useI18n()
const message = useMessage()

const blank = (): NodeForm => ({
  name: '', host: '', port: 22, username: 'root', authType: 'key', password: '', privateKey: '', passphrase: '', hostKey: '', color: '',
})
const form = ref<NodeForm>(blank())
const scanned = ref<HostKeyScan | null>(null)
const scannedFor = ref('')
const confirmed = ref(false)
const busy = ref<'' | 'scan' | 'test' | 'save'>('')
const error = ref('')
const testOK = ref(false)

const isLocal = computed(() => props.edit?.kind === 'local')
const addr = computed(() => `${form.value.host.trim()}:${form.value.port}`)
// 新建时必须确认指纹；修改时地址或端口变化也要重新确认
const needScan = computed(() => {
  if (isLocal.value) return false
  if (!props.edit) return true
  return form.value.host.trim() !== props.edit.host || form.value.port !== props.edit.port
})
const scanValid = computed(() => !!scanned.value && scannedFor.value === addr.value)

watch(
  () => props.show,
  (s) => {
    if (!s) return
    error.value = ''
    testOK.value = false
    scanned.value = null
    confirmed.value = false
    const e = props.edit
    form.value = e
      ? { ...blank(), name: e.name, host: e.host, port: e.port || 22, username: e.username, authType: (e.authType || 'key') as 'key', color: e.color }
      : blank()
  },
)

async function scan() {
  error.value = ''
  busy.value = 'scan'
  try {
    scanned.value = await api.post<HostKeyScan>('/nodes/scan', { host: form.value.host.trim(), port: form.value.port })
    scannedFor.value = addr.value
    confirmed.value = false
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = ''
  }
}

function payload(): NodeForm {
  return { ...form.value, hostKey: scanValid.value && confirmed.value ? scanned.value!.key : '' }
}

function ready(): boolean {
  if (isLocal.value) return !!form.value.name.trim()
  if (!form.value.host.trim() || !form.value.username.trim()) {
    error.value = '请填写地址和用户名'
    return false
  }
  if (needScan.value && !(scanValid.value && confirmed.value)) {
    error.value = '请先获取并核对主机指纹'
    return false
  }
  return true
}

async function readKeyFile(ev: Event) {
  const f = (ev.target as HTMLInputElement).files?.[0]
  if (f) form.value.privateKey = await f.text()
  ;(ev.target as HTMLInputElement).value = ''
}

async function test() {
  error.value = ''
  testOK.value = false
  if (!ready()) return
  if (!(form.value.authType === 'key' ? form.value.privateKey.trim() : form.value.password)) {
    error.value = '请填写私钥或密码'
    return
  }
  busy.value = 'test'
  try {
    await api.post('/nodes/test', payload())
    testOK.value = true
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = ''
  }
}

async function save() {
  error.value = ''
  if (!ready()) return
  busy.value = 'save'
  try {
    const v = props.edit ? await api.put<NodeView>(`/nodes/${props.edit.id}`, payload()) : await api.post<NodeView>('/nodes', payload())
    message.success(props.edit ? t('common.saved') : t('servers.added'))
    emit('saved', v)
    emit('update:show', false)
  } catch (e) {
    error.value = errorText(e)
  } finally {
    busy.value = ''
  }
}
</script>

<template>
  <NModal
    :show="show"
    preset="card"
    :title="edit ? t('servers.editTitle', { name: edit.name }) : t('servers.add')"
    style="width: 560px; max-width: calc(100vw - 32px)"
    :mask-closable="false"
    data-testid="node-dialog"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <NForm label-placement="left" label-width="96" size="small" @submit.prevent>
      <NFormItem :label="t('servers.name')">
        <NInput v-model:value="form.name" :placeholder="t('servers.namePh')" maxlength="64" data-testid="node-name" />
      </NFormItem>
      <NFormItem :label="t('servers.color')">
        <NColorPicker v-model:value="form.color" :show-alpha="false" :modes="['hex']" :swatches="['#8764b8', '#0078d4', '#038387', '#ca5010', '#498205', '#c239b3', '#986f0b', '#e74856']" style="width: 120px" />
        <NButton v-if="form.color" text size="tiny" style="margin-left: 8px" @click="form.color = ''">{{ t('servers.autoColor') }}</NButton>
      </NFormItem>
      <template v-if="!isLocal">
        <NFormItem :label="t('servers.address')">
          <div class="addr">
            <NInput v-model:value="form.host" placeholder="203.0.113.10" data-testid="node-host" />
            <NInputNumber v-model:value="form.port" :min="1" :max="65535" :show-button="false" style="width: 90px" data-testid="node-port" />
          </div>
        </NFormItem>
        <NFormItem :label="t('servers.username')">
          <NInput v-model:value="form.username" data-testid="node-user" />
        </NFormItem>

        <NFormItem :label="t('servers.fingerprint')">
          <div class="fp">
            <div v-if="edit?.hostKey && !needScan" class="sd-mono small">{{ edit.hostKey.type }} {{ edit.hostKey.fingerprint }}</div>
            <template v-if="scanValid">
              <div class="sd-mono small" data-testid="node-fingerprint">{{ scanned!.keyType }} {{ scanned!.fingerprint }}</div>
              <NCheckbox v-model:checked="confirmed" data-testid="node-fp-confirm">{{ t('servers.fpConfirm') }}</NCheckbox>
            </template>
            <NButton v-if="needScan || scanned" size="small" :loading="busy === 'scan'" data-testid="node-scan" @click="scan">
              {{ scanValid ? t('servers.rescan') : t('servers.scan') }}
            </NButton>
            <div v-if="needScan && !scanValid" class="sd-muted small">{{ t('servers.fpHint') }}</div>
          </div>
        </NFormItem>

        <NFormItem :label="t('servers.auth')">
          <NRadioGroup v-model:value="form.authType" size="small">
            <NRadioButton value="key" data-testid="node-auth-key">{{ t('servers.authKey') }}</NRadioButton>
            <NRadioButton value="password" data-testid="node-auth-password">{{ t('servers.authPassword') }}</NRadioButton>
          </NRadioGroup>
        </NFormItem>
        <template v-if="form.authType === 'key'">
          <NFormItem :label="t('servers.privateKey')">
            <div class="keybox">
              <NInput
                v-model:value="form.privateKey"
                type="textarea"
                :rows="4"
                class="sd-mono"
                :placeholder="edit ? t('servers.keepCred') : '-----BEGIN OPENSSH PRIVATE KEY-----'"
                data-testid="node-key"
              />
              <label class="file sd-muted small">
                {{ t('servers.keyFile') }}
                <input type="file" @change="readKeyFile" />
              </label>
            </div>
          </NFormItem>
          <NFormItem :label="t('servers.passphrase')">
            <NInput v-model:value="form.passphrase" type="password" show-password-on="click" :placeholder="t('servers.passphrasePh')" />
          </NFormItem>
        </template>
        <NFormItem v-else :label="t('servers.password')">
          <NInput
            v-model:value="form.password"
            type="password"
            show-password-on="click"
            :placeholder="edit ? t('servers.keepCred') : ''"
            data-testid="node-password"
          />
        </NFormItem>
        <div class="sd-muted small note">{{ t('servers.credNote') }}</div>
      </template>
    </NForm>
    <NAlert v-if="error" type="error" :bordered="false" class="msg" data-testid="node-error">{{ error }}</NAlert>
    <NAlert v-else-if="testOK" type="success" :bordered="false" class="msg">{{ t('servers.testOK') }}</NAlert>
    <template #footer>
      <div class="foot">
        <NButton v-if="!edit" :loading="busy === 'test'" :disabled="!!busy" data-testid="node-test" @click="test">{{ t('servers.test') }}</NButton>
        <span class="grow" />
        <NButton @click="emit('update:show', false)">{{ t('common.cancel') }}</NButton>
        <NButton type="primary" :loading="busy === 'save'" :disabled="!!busy" data-testid="node-save" @click="save">{{ t('common.save') }}</NButton>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.addr {
  display: flex;
  gap: 6px;
  width: 100%;
}
.fp {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 6px;
  min-width: 0;
}
.small {
  font-size: 12px;
  word-break: break-all;
}
.keybox {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.file {
  cursor: pointer;
  text-decoration: underline;
}
.file input {
  display: none;
}
.note {
  margin: -4px 0 4px 96px;
}
.msg {
  margin-top: 8px;
}
.foot {
  display: flex;
  gap: 8px;
}
.grow {
  flex: 1;
}
</style>
