<script setup lang="ts">
// 面板访问方式：仅本机（经 SSH 隧道访问）或公网（域名 + HTTPS）。切换后 60 秒内要在新地址确认，
// 否则自动回到原来的方式，避免证书或网络问题导致面板失联。
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCheckbox, NInput, NInputNumber, NModal, NRadioButton, NRadioGroup, NSelect, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { Certificate, PanelAccess, PendingChange } from '@/api/types'
import { useSession } from '@/stores/session'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import { errorText } from '@/i18n'

const { t } = useI18n()
const message = useMessage()
const session = useSession()
const hostInfo = useHost()

const cur = ref<PanelAccess | null>(null)
const certs = ref<Certificate[]>([])
const form = ref({ https: false, domain: '', certId: 0, port: 33333, openFirewall: true })

async function load() {
  try {
    cur.value = await api.get<PanelAccess>('/panel/access')
    form.value = { https: cur.value.https, domain: cur.value.domain, certId: cur.value.certId, port: cur.value.port, openFirewall: true }
  } catch (e) {
    message.error(errorText(e))
  }
  if (session.has('website:view')) {
    try {
      certs.value = await api.get<Certificate[]>('/nodes/local/certs')
    } catch {
      certs.value = []
    }
  }
}
onMounted(load)

const certOptions = computed(() =>
  certs.value.filter((c) => c.status === 'valid').map((c) => ({ value: c.id, label: `${c.domains}（${t('websites.daysLeft', { n: c.daysLeft })}）` })),
)
const current = computed(() => {
  const c = cur.value
  if (!c) return ''
  return c.public ? `https://${c.domain}:${c.port}` : `http://127.0.0.1:${c.port}`
})

/* ---------- 切换后的确认 ---------- */
const switching = ref<{ change: PendingChange; url: string; confirmUrl: string } | null>(null)
const now = ref(Date.now())
const timer = window.setInterval(() => (now.value = Date.now()), 500)
onBeforeUnmount(() => clearInterval(timer))
const left = computed(() => (switching.value ? Math.max(0, Math.ceil((Date.parse(switching.value.change.deadline) - now.value) / 1000)) : 0))

async function apply() {
  const target = form.value.https ? `https://${form.value.domain}:${form.value.port}` : `http://127.0.0.1:${form.value.port}`
  try {
    const r = await withDanger(
      {
        level: 2,
        action: t('access.change'),
        node: hostInfo.label,
        impact: [t('access.from', { url: current.value }), t('access.to', { url: target }), t('access.confirmImpact'), form.value.https ? '' : t('access.localImpact')].filter(Boolean),
        confirmText: t('access.change'),
      },
      (pw) => api.put<{ pending: PendingChange; url: string; confirmUrl: string }>('/panel/access', form.value, { confirmPassword: pw }),
    )
    if (r) switching.value = { change: r.pending, url: r.url, confirmUrl: r.confirmUrl }
  } catch (e) {
    message.error(errorText(e))
  }
}

function goNew() {
  if (switching.value) window.location.href = switching.value.confirmUrl
}
</script>

<template>
  <div class="card" data-testid="panel-access">
    <div class="title">{{ t('access.title') }}</div>
    <div class="sd-muted small">{{ t('access.current', { url: current }) }}</div>
    <NAlert v-if="cur?.pending" type="warning" :bordered="false">{{ t('access.pendingNow', { user: cur.pending.username }) }}</NAlert>
    <div class="row">
      <span>{{ t('access.mode') }}</span>
      <NRadioGroup v-model:value="form.https" size="small">
        <NRadioButton :value="false">{{ t('access.local') }}</NRadioButton>
        <NRadioButton :value="true">{{ t('access.public') }}</NRadioButton>
      </NRadioGroup>
    </div>
    <div class="sd-muted small">{{ form.https ? t('access.publicHint') : t('access.localHint') }}</div>
    <template v-if="form.https">
      <div class="row">
        <span>{{ t('access.domain') }}</span>
        <NInput v-model:value="form.domain" size="small" placeholder="panel.example.com" style="width: 260px" data-testid="access-domain" />
      </div>
      <div class="row">
        <span>{{ t('access.cert') }}</span>
        <NSelect v-model:value="form.certId" :options="certOptions" size="small" style="width: 320px" :placeholder="t('access.certPh')" data-testid="access-cert" />
      </div>
      <div class="sd-muted small">{{ t('access.certHint') }}</div>
    </template>
    <div class="row">
      <span>{{ t('access.port') }}</span>
      <NInputNumber v-model:value="form.port" :min="1" :max="65535" size="small" style="width: 140px" data-testid="access-port" />
    </div>
    <div v-if="form.https" class="row">
      <NCheckbox v-model:checked="form.openFirewall">{{ t('access.openFirewall') }}</NCheckbox>
    </div>
    <div>
      <NButton type="primary" size="small" :disabled="!!cur?.pending || (form.https && (!form.domain.trim() || !form.certId))" data-testid="access-apply" @click="apply">{{ t('access.change') }}</NButton>
    </div>

    <NModal :show="!!switching" preset="card" :title="t('access.switching')" :closable="false" :mask-closable="false" style="width: 520px; max-width: calc(100vw - 32px)">
      <template v-if="switching">
        <p>{{ t('access.switchingText', { n: left }) }}</p>
        <p class="sd-mono url">{{ switching.url }}</p>
        <p class="sd-muted small">{{ t('access.switchingHint', { old: current }) }}</p>
      </template>
      <template #footer>
        <div class="foot">
          <NButton type="primary" :disabled="left === 0" data-testid="access-go" @click="goNew">{{ t('access.goNew') }}</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.card {
  background: var(--sd-surface-2);
  border: 1px solid var(--sd-border);
  border-radius: var(--sd-radius);
  padding: 14px 16px;
  margin: 16px 0 10px;
  max-width: 820px;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.title {
  font-weight: 600;
  font-size: 14px;
}
.small {
  font-size: 12px;
}
.row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 4px 0;
  font-size: 13px;
}
.url {
  font-size: 15px;
  word-break: break-all;
}
.foot {
  display: flex;
  justify-content: flex-end;
}
</style>
