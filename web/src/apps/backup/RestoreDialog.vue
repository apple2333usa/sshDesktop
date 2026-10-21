<script setup lang="ts">
// 恢复确认：选择恢复位置（目录备份）、填写备份密码、是否先做快照，二级确认后以后台任务执行。
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCheckbox, NForm, NFormItem, NInput, NModal, NRadio, NRadioGroup, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { Task } from '@/api/types'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import { errorText } from '@/i18n'
import type { RestoreSource } from './types'

const props = defineProps<{ source: RestoreSource | null }>()
const emit = defineEmits<{ close: []; task: [Task] }>()
const { t } = useI18n()
const message = useMessage()
const hostInfo = useHost()

const where = ref<'origin' | 'other'>('origin')
const dest = ref('')
const password = ref('')
const noSnapshot = ref(false)

watch(
  () => props.source,
  (s) => {
    if (!s) return
    where.value = 'origin'
    dest.value = ''
    password.value = ''
    noSnapshot.value = !!s.snapshot
  },
)

const needPassword = computed(() => !!props.source?.encrypted && !props.source.savedPassword)
const name = computed(() => props.source?.key.split('/').pop() ?? '')

function impact(s: RestoreSource): string[] {
  const out = [name.value]
  if (s.type === 'dir') out.push(t('backup.restoreDirImpact', { dir: where.value === 'other' ? dest.value : s.source || t('backup.originalDir') }))
  else if (s.type === 'app') out.push(t('backup.restoreAppImpact', { name: s.source }))
  else if (s.type === 'website') out.push(t('backup.restoreSiteImpact', { name: s.source }))
  else if (s.type === 'panel') out.push(t('backup.restorePanelImpact'))
  else out.push(t('backup.restoreUnknownImpact'))
  out.push(noSnapshot.value ? t('backup.noSnapshotImpact') : t('backup.snapshotImpact'))
  return out
}

async function run() {
  const s = props.source
  if (!s) return
  try {
    const task = await withDanger(
      { level: 2, action: t('backup.restoreAction'), node: hostInfo.label, impact: impact(s), confirmText: t('backup.restore') },
      (pw) =>
        api.post<Task>(
          '/nodes/local/backup/restore',
          {
            recordId: s.recordId ?? 0,
            targetId: s.targetId ?? 0,
            key: s.recordId ? '' : s.key,
            password: password.value,
            dest: where.value === 'other' ? dest.value.trim() : '',
            noSnapshot: noSnapshot.value,
          },
          { confirmPassword: pw },
        ),
    )
    if (task) {
      emit('task', task)
      emit('close')
    }
  } catch (e) {
    message.error(errorText(e))
  }
}
</script>

<template>
  <NModal :show="!!source" preset="card" :title="t('backup.restore') + ' · ' + name" style="width: 560px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && emit('close')">
    <NForm v-if="source" label-placement="left" label-width="96" size="small">
      <NFormItem :label="t('backup.content')">
        <span v-if="source.type">{{ t('backup.type.' + source.type) }}<span v-if="source.source" class="sd-mono">：{{ source.source }}</span></span>
        <span v-else class="sd-muted">{{ t('backup.typeFromFile') }}</span>
      </NFormItem>
      <NFormItem v-if="source.type === 'dir' || !source.type" :label="t('backup.restoreTo')">
        <div class="col">
          <NRadioGroup v-model:value="where">
            <NRadio value="origin">{{ t('backup.toOrigin') }}</NRadio>
            <NRadio value="other">{{ t('backup.toOther') }}</NRadio>
          </NRadioGroup>
          <NInput v-if="where === 'other'" v-model:value="dest" class="sd-mono" placeholder="/root/restored" data-testid="restore-dest" />
          <span class="sd-muted small">{{ t('backup.restoreToHint') }}</span>
        </div>
      </NFormItem>
      <NFormItem v-if="source.encrypted" :label="t('backup.password')" :required="needPassword">
        <div class="col">
          <NInput v-model:value="password" type="password" show-password-on="click" :input-props="{ autocomplete: 'new-password' }" data-testid="restore-password" />
          <span v-if="source.savedPassword" class="sd-muted small">{{ t('backup.savedPasswordHint') }}</span>
        </div>
      </NFormItem>
      <NFormItem :label="t('backup.snapshot')">
        <NCheckbox v-model:checked="noSnapshot">{{ t('backup.noSnapshot') }}</NCheckbox>
      </NFormItem>
      <NAlert v-if="source.type === 'panel'" type="warning" :bordered="false">{{ t('backup.panelRestoreWarn') }}</NAlert>
      <NAlert v-else-if="source.type === 'app'" type="info" :bordered="false">{{ t('backup.appRestoreHint') }}</NAlert>
      <NAlert v-else-if="source.type === 'website'" type="info" :bordered="false">{{ t('backup.siteRestoreHint') }}</NAlert>
    </NForm>
    <template #footer>
      <div class="foot">
        <NButton @click="emit('close')">{{ t('common.cancel') }}</NButton>
        <NButton
          type="primary"
          :disabled="(needPassword && !password) || (where === 'other' && !dest.trim().startsWith('/'))"
          data-testid="restore-ok"
          @click="run"
          >{{ t('backup.restore') }}</NButton
        >
      </div>
    </template>
  </NModal>
</template>

<style scoped>
.col {
  display: flex;
  flex-direction: column;
  gap: 6px;
  width: 100%;
}
.small {
  font-size: 12px;
}
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
</style>
