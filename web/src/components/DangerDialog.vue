<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { NButton, NInput, NModal } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { dangerState } from './danger'
import { useHost } from '@/stores/host'

const { t } = useI18n()
const host = useHost()
const password = ref('')
const input = ref<InstanceType<typeof NInput> | null>(null)

const impactList = computed(() => {
  const v = dangerState.impact
  if (!v) return []
  return Array.isArray(v) ? v : [v]
})

watch(
  () => dangerState.show,
  async (s) => {
    if (s) {
      password.value = ''
      await nextTick()
      input.value?.focus()
    }
  },
)

function finish(ok: boolean) {
  if (ok && dangerState.level === 2 && !password.value) return
  const resolve = dangerState.resolve
  dangerState.show = false
  dangerState.resolve = null
  resolve?.(ok ? (dangerState.level === 2 ? password.value : '') : null)
}
</script>

<template>
  <NModal :show="dangerState.show" :mask-closable="false" :auto-focus="false" @esc="finish(false)">
    <div class="danger" :class="'lv' + dangerState.level" role="alertdialog" data-testid="danger-dialog">
      <div class="bar">
        {{ dangerState.level === 2 ? t('danger.level2') : t('danger.level1') }}
      </div>
      <div class="body">
        <div class="action">{{ dangerState.action }}</div>
        <div class="row">
          <span class="k">{{ t('danger.node') }}</span>
          <span class="v">{{ dangerState.node ?? host.label }}</span>
        </div>
        <div v-if="impactList.length" class="row col">
          <span class="k">{{ t('danger.impact', { n: impactList.length }) }}</span>
          <div class="impact sd-scroll">
            <div v-for="(x, i) in impactList" :key="i" class="sd-mono">{{ x }}</div>
          </div>
        </div>
        <div v-if="dangerState.level === 2" class="row col">
          <span class="k">{{ t('danger.password') }}</span>
          <NInput
            ref="input"
            v-model:value="password"
            type="password"
            show-password-on="click"
            :placeholder="t('danger.passwordPlaceholder')"
            data-testid="danger-password"
            @keyup.enter="finish(true)"
          />
        </div>
        <div v-if="dangerState.error" class="error">{{ dangerState.error }}</div>
      </div>
      <div class="foot">
        <NButton data-testid="danger-cancel" @click="finish(false)">{{ t('common.cancel') }}</NButton>
        <NButton
          type="error"
          :disabled="dangerState.level === 2 && !password"
          data-testid="danger-confirm"
          @click="finish(true)"
        >
          {{ dangerState.confirmText }}
        </NButton>
      </div>
    </div>
  </NModal>
</template>

<style scoped>
.danger {
  width: min(520px, calc(100vw - 32px));
  background: var(--sd-surface);
  color: var(--sd-text);
  border-radius: 8px;
  overflow: hidden;
  box-shadow: var(--sd-shadow);
  border: 1px solid var(--sd-border);
}
.bar {
  padding: 10px 20px;
  font-weight: 600;
  color: #fff;
  background: #c75b00;
}
.lv2 .bar {
  background: #c42b1c;
}
.body {
  padding: 18px 20px 8px;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.action {
  font-size: 22px;
  font-weight: 600;
  line-height: 1.3;
}
.row {
  display: flex;
  gap: 12px;
  align-items: baseline;
}
.row.col {
  flex-direction: column;
  align-items: stretch;
  gap: 6px;
}
.k {
  color: var(--sd-text-2);
  flex-shrink: 0;
}
.v {
  font-weight: 600;
}
.impact {
  max-height: 160px;
  padding: 8px 10px;
  border-radius: 4px;
  background: var(--sd-hover);
  border: 1px solid var(--sd-border);
  font-size: 13px;
  word-break: break-all;
}
.error {
  color: var(--sd-danger);
}
.foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 14px 20px;
  background: var(--sd-bg);
  border-top: 1px solid var(--sd-border);
}
</style>
