<script setup lang="ts">
// 新建 / 修改站点。保存时由服务器生成配置、用 nginx -t 校验后重新加载，校验失败不会改动现有配置。
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NCheckbox, NCollapseTransition, NForm, NFormItem, NInput, NInputNumber, NModal, NRadioButton, NRadioGroup, NSelect, NSwitch, useMessage,
} from 'naive-ui'
import { api } from '@/api/http'
import type { Certificate, Task, WebRuntimeStatus, Website } from '@/api/types'
import { useNotify } from '@/stores/notify'
import { errorText } from '@/i18n'

const props = defineProps<{ site: Partial<Website>; runtime: WebRuntimeStatus }>()
const emit = defineEmits<{ close: []; saved: [] }>()
const { t } = useI18n()
const message = useMessage()
const notify = useNotify()
const base = '/nodes/local/websites'

const editing = !!props.site.id
const form = ref<Partial<Website>>({
  type: 'proxy', name: '', domains: '', proxyPass: '', proxyHost: '', webSocket: true, root: '', index: '',
  httpPort: 80, httpsPort: 443, https: false, certId: 0, forceHttps: true, http2: true, hsts: false, accessLog: true,
  custom: '', raw: '', enabled: true, remark: '',
  ...props.site,
})
const isRaw = computed(() => !!form.value.raw?.trim())
// 未启用 HTTPS 的站点保存时会清掉强制 HTTPS 等选项，重新打开 HTTPS 时恢复默认值
watch(
  () => form.value.https,
  (on) => {
    if (on && !props.site.https) Object.assign(form.value, { forceHttps: true, http2: true })
  },
)

const certs = ref<Certificate[]>([])
async function loadCerts() {
  try {
    certs.value = await api.get<Certificate[]>('/nodes/local/certs')
  } catch {
    certs.value = []
  }
}
onMounted(loadCerts)
const certOptions = computed(() =>
  certs.value
    .filter((c) => c.status === 'valid' || c.id === form.value.certId)
    .map((c) => ({ value: c.id, label: `${c.domains}（${t('websites.daysLeft', { n: c.daysLeft })}）` })),
)

/* 用站点的域名申请 Let's Encrypt 证书（HTTP 验证），完成后自动选中 */
const issuing = ref(false)
async function issueCert() {
  issuing.value = true
  try {
    const r = await api.post<{ cert: Certificate; task: Task }>('/nodes/local/certs', {
      domains: form.value.domains, ca: 'letsencrypt', challenge: 'http', keyType: 'ec256', autoRenew: true,
    })
    const done = await notify.waitTask(r.task)
    await loadCerts()
    if (done.status === 'success') {
      form.value.certId = r.cert.id
      message.success(t('websites.certIssued'))
    } else message.error(done.error || t('websites.certFailed'))
  } catch (e) {
    message.error(errorText(e))
  } finally {
    issuing.value = false
  }
}

const preview = ref('')
async function showPreview() {
  try {
    preview.value = (await api.post<{ conf: string }>(base + '/preview', form.value)).conf
  } catch (e) {
    message.error(errorText(e))
  }
}

const saving = ref(false)
async function save() {
  saving.value = true
  try {
    if (editing) await api.put(`${base}/sites/${props.site.id}`, form.value)
    else await api.post(base + '/sites', form.value)
    message.success(t('common.saved'))
    emit('saved')
  } catch (e) {
    message.error(errorText(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <NModal :show="true" preset="card" :title="editing ? t('websites.edit', { name: site.name }) : t('websites.add')" style="width: 720px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && emit('close')">
    <NForm label-placement="left" label-width="110" size="small" class="form sd-scroll">
      <NFormItem v-if="!isRaw" :label="t('websites.typeCol')">
        <NRadioGroup v-model:value="form.type" size="small">
          <NRadioButton value="proxy">{{ t('websites.type.proxy') }}</NRadioButton>
          <NRadioButton value="static">{{ t('websites.type.static') }}</NRadioButton>
        </NRadioGroup>
      </NFormItem>
      <NFormItem :label="t('websites.domains')" required>
        <NInput v-model:value="form.domains" type="textarea" :autosize="{ minRows: 1, maxRows: 4 }" :placeholder="t('websites.domainsPh')" data-testid="site-domains" />
      </NFormItem>
      <NFormItem :label="t('websites.name')">
        <NInput v-model:value="form.name" :disabled="editing" :placeholder="t('websites.namePh')" maxlength="64" data-testid="site-name" />
      </NFormItem>
      <template v-if="!isRaw && form.type === 'proxy'">
        <NFormItem :label="t('websites.proxyPass')" required>
          <NInput v-model:value="form.proxyPass" placeholder="http://127.0.0.1:8080" data-testid="site-proxy" />
        </NFormItem>
        <NFormItem :label="t('websites.proxyHost')">
          <NInput v-model:value="form.proxyHost" :placeholder="t('websites.proxyHostPh')" />
        </NFormItem>
        <NFormItem :label="t('websites.webSocket')">
          <NSwitch v-model:value="form.webSocket" />
        </NFormItem>
      </template>
      <template v-if="!isRaw && form.type === 'static'">
        <NFormItem :label="t('websites.root')">
          <NInput v-model:value="form.root" :placeholder="t('websites.rootPh')" />
        </NFormItem>
        <NFormItem :label="t('websites.index')">
          <NInput v-model:value="form.index" placeholder="index.html index.htm" />
        </NFormItem>
      </template>
      <NFormItem :label="t('websites.ports')">
        <span class="small">HTTP</span>
        <NInputNumber v-model:value="form.httpPort" :min="1" :max="65535" style="width: 110px; margin: 0 12px 0 6px" />
        <span class="small">HTTPS</span>
        <NInputNumber v-model:value="form.httpsPort" :min="1" :max="65535" style="width: 110px; margin-left: 6px" />
      </NFormItem>
      <NFormItem label="HTTPS">
        <NSwitch v-model:value="form.https" data-testid="site-https" />
      </NFormItem>
      <NCollapseTransition :show="!!form.https">
        <NFormItem :label="t('websites.cert')">
          <NSelect v-model:value="form.certId" :options="certOptions" :placeholder="t('websites.certPh')" style="flex: 1" data-testid="site-cert" />
          <NButton style="margin-left: 8px" :loading="issuing" :disabled="!form.domains?.trim()" @click="issueCert">{{ t('websites.issueCert') }}</NButton>
        </NFormItem>
        <NFormItem label=" ">
          <NCheckbox v-model:checked="form.forceHttps">{{ t('websites.forceHttps') }}</NCheckbox>
          <NCheckbox v-model:checked="form.http2">HTTP/2</NCheckbox>
          <NCheckbox v-model:checked="form.hsts" :disabled="!form.forceHttps">HSTS</NCheckbox>
        </NFormItem>
      </NCollapseTransition>
      <NFormItem :label="t('websites.accessLog')">
        <NSwitch v-model:value="form.accessLog" />
      </NFormItem>
      <NFormItem v-if="!isRaw" :label="t('websites.custom')">
        <NInput v-model:value="form.custom" type="textarea" class="code" :autosize="{ minRows: 2, maxRows: 10 }" :placeholder="t('websites.customPh')" />
      </NFormItem>
      <NFormItem v-if="isRaw" :label="t('websites.rawConf')">
        <NInput v-model:value="form.raw" type="textarea" class="code" :autosize="{ minRows: 8, maxRows: 24 }" />
      </NFormItem>
      <NFormItem :label="t('websites.remark')">
        <NInput v-model:value="form.remark" maxlength="128" />
      </NFormItem>
      <pre v-if="preview" class="preview sd-mono">{{ preview }}</pre>
    </NForm>
    <template #footer>
      <div class="foot">
        <NButton quaternary @click="showPreview">{{ t('websites.preview') }}</NButton>
        <span style="flex: 1" />
        <NButton @click="emit('close')">{{ t('common.cancel') }}</NButton>
        <NButton type="primary" :loading="saving" :disabled="!form.domains?.trim()" data-testid="site-save" @click="save">{{ t('common.save') }}</NButton>
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.form {
  max-height: 66vh;
  padding-right: 6px;
}
.small {
  font-size: 12px;
}
.code :deep(textarea) {
  font-family: var(--sd-mono);
  font-size: 12px;
}
.preview {
  font-size: 12px;
  background: var(--sd-bg);
  padding: 8px;
  border-radius: 4px;
  max-height: 300px;
  overflow: auto;
  margin: 0;
}
.foot {
  display: flex;
  gap: 8px;
}
</style>
