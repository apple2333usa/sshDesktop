<script setup lang="ts">
// 系统 crontab：/etc/crontab、/etc/cron.d/*、各用户的 crontab。可以启用 / 停用、修改、删除、新增一行，或按原始文本编辑。
// 停用的行在行首加 “#sshdesktop-off# ”，文件第一次修改前备份到面板数据目录。
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NAlert, NButton, NCollapse, NCollapseItem, NForm, NFormItem, NInput, NModal, NSwitch, NTag, useMessage } from 'naive-ui'
import { api } from '@/api/http'
import type { CrontabFile, CrontabLine } from '@/api/types'
import { useSession } from '@/stores/session'
import { useHost } from '@/stores/host'
import { withDanger } from '@/components/danger'
import { errorText, formatTime } from '@/i18n'
import ScheduleInput from './ScheduleInput.vue'

const { t } = useI18n()
const message = useMessage()
const session = useSession()
const hostInfo = useHost()
const canManage = computed(() => session.has('cron:manage'))
const base = '/nodes/local/cron/system'

const files = ref<CrontabFile[]>([])
const loading = ref(false)
const expanded = ref<string[]>([])

async function load() {
  loading.value = true
  try {
    files.value = await api.get<CrontabFile[]>(base)
    if (!expanded.value.length) expanded.value = files.value.filter((f) => f.lines.some((l) => l.kind === 'job')).map((f) => f.path)
  } catch (e) {
    message.error(errorText(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

function replace(f: CrontabFile) {
  const i = files.value.findIndex((x) => x.path === f.path)
  if (i >= 0) files.value[i] = f
}

async function line(f: CrontabFile, body: Record<string, unknown>, danger?: { action: string; impact: string }) {
  try {
    const run = () => api.post<CrontabFile>(base + '/line', { path: f.path, hash: f.hash, ...body })
    const r = danger
      ? await withDanger({ level: 1, action: danger.action, node: hostInfo.label, impact: [f.path, danger.impact], confirmText: danger.action }, run)
      : await run()
    if (r) replace(r)
  } catch (e) {
    message.error(errorText(e))
    load()
  }
}

const jobs = (f: CrontabFile) => f.lines.filter((l) => l.kind === 'job')

/* ---------- 修改与新增一行 ---------- */
const lineForm = ref<{ file: CrontabFile; no: number; schedule: string; user: string; command: string } | null>(null)
function openLine(f: CrontabFile, l?: CrontabLine) {
  lineForm.value = { file: f, no: l?.no ?? 0, schedule: l?.schedule ?? '0 3 * * *', user: l?.user ?? 'root', command: l?.command ?? '' }
}
async function saveLine() {
  const v = lineForm.value
  if (!v) return
  lineForm.value = null
  await line(v.file, { no: v.no, action: v.no ? 'update' : 'add', schedule: v.schedule, user: v.user, command: v.command })
}

/* ---------- 原始文本 ---------- */
const raw = ref<{ path: string; content: string; hash: string } | null>(null)
async function openRaw(f: CrontabFile) {
  try {
    raw.value = await api.get(base + '/file', { path: f.path })
  } catch (e) {
    message.error(errorText(e))
  }
}
async function saveRaw() {
  const v = raw.value
  if (!v) return
  try {
    const r = await withDanger(
      { level: 1, action: t('cron.saveFile'), node: hostInfo.label, impact: [v.path, t('cron.saveFileImpact')], confirmText: t('common.save') },
      () => api.put<CrontabFile>(base + '/file', v),
    )
    if (r) {
      raw.value = null
      replace(r)
    }
  } catch (e) {
    message.error(errorText(e))
  }
}
</script>

<template>
  <div class="sys">
    <div class="sd-toolbar">
      <span class="sd-muted small">{{ t('cron.systemHint') }}</span>
      <span style="flex: 1" />
      <NButton size="small" quaternary @click="load">{{ t('explorer.refresh') }}</NButton>
    </div>
    <div class="body sd-scroll">
      <NCollapse v-model:expanded-names="expanded">
        <NCollapseItem v-for="f in files" :key="f.path" :name="f.path">
          <template #header>
            <span class="sd-mono">{{ f.path }}</span>
            <NTag v-if="f.user" size="tiny" :bordered="false" style="margin-left: 8px">{{ t('cron.userCrontab', { user: f.user }) }}</NTag>
            <span class="sd-muted small" style="margin-left: 8px">{{ t('cron.jobCount', { n: jobs(f).length }) }}</span>
          </template>
          <template #header-extra>
            <template v-if="canManage && !f.readOnly">
              <NButton size="tiny" quaternary @click.stop="openLine(f)">{{ t('cron.addLine') }}</NButton>
              <NButton size="tiny" quaternary @click.stop="openRaw(f)">{{ t('cron.editRaw') }}</NButton>
            </template>
          </template>
          <NAlert v-if="f.error" type="error" :bordered="false">{{ f.error }}</NAlert>
          <div v-for="l in jobs(f)" :key="l.no" class="line" :class="{ off: !l.enabled }" :data-testid="'sys-line-' + l.no">
            <NSwitch size="small" :value="l.enabled" :disabled="!canManage || f.readOnly" @update:value="() => line(f, { no: l.no, action: 'toggle' })" />
            <span class="sd-mono sched">{{ l.schedule }}</span>
            <span v-if="f.system" class="sd-mono user">{{ l.user }}</span>
            <span class="sd-mono cmd" :title="l.command">{{ l.command }}</span>
            <span class="sd-muted small next">{{ l.next ? formatTime(l.next * 1000) : '' }}</span>
            <template v-if="canManage && !f.readOnly">
              <NButton size="tiny" quaternary @click="openLine(f, l)">{{ t('common.edit') }}</NButton>
              <NButton size="tiny" quaternary type="error" @click="line(f, { no: l.no, action: 'delete' }, { action: t('cron.deleteLine'), impact: l.raw })">{{ t('common.delete') }}</NButton>
            </template>
          </div>
          <div v-if="!jobs(f).length" class="sd-muted small">{{ t('cron.noLines') }}</div>
        </NCollapseItem>
      </NCollapse>
      <div v-if="!files.length && !loading" class="sd-muted empty">{{ t('cron.noCrontab') }}</div>
    </div>

    <NModal :show="!!lineForm" preset="card" :title="lineForm?.no ? t('cron.editLine') : t('cron.addLine')" style="width: 560px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && (lineForm = null)">
      <NForm v-if="lineForm" label-placement="left" label-width="80" size="small">
        <NFormItem :label="t('cron.schedule')">
          <ScheduleInput v-model:value="lineForm.schedule" />
        </NFormItem>
        <NFormItem v-if="lineForm.file.system" :label="t('cron.user')">
          <NInput v-model:value="lineForm.user" style="width: 160px" />
        </NFormItem>
        <NFormItem :label="t('cron.command')">
          <NInput v-model:value="lineForm.command" class="sd-mono" />
        </NFormItem>
      </NForm>
      <template #footer>
        <div class="foot">
          <NButton @click="lineForm = null">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" :disabled="!lineForm?.command.trim()" @click="saveLine">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>

    <NModal :show="!!raw" preset="card" :title="raw?.path" style="width: 760px; max-width: calc(100vw - 32px)" @update:show="(v: boolean) => !v && (raw = null)">
      <NInput v-if="raw" v-model:value="raw.content" type="textarea" class="code" :autosize="{ minRows: 12, maxRows: 26 }" />
      <template #footer>
        <div class="foot">
          <NButton @click="raw = null">{{ t('common.cancel') }}</NButton>
          <NButton type="primary" @click="saveRaw">{{ t('common.save') }}</NButton>
        </div>
      </template>
    </NModal>
  </div>
</template>

<style scoped>
.sys {
  height: 100%;
  display: flex;
  flex-direction: column;
}
.body {
  flex: 1;
  min-height: 0;
  padding: 8px 12px;
}
.small {
  font-size: 12px;
}
.line {
  display: flex;
  gap: 10px;
  align-items: center;
  padding: 4px 0;
  border-top: 1px solid var(--sd-border);
  min-width: 0;
}
.line.off .cmd,
.line.off .sched {
  opacity: 0.55;
  text-decoration: line-through;
}
.sched {
  width: 130px;
  flex: none;
}
.user {
  width: 70px;
  flex: none;
}
.cmd {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 12px;
}
.next {
  width: 120px;
  flex: none;
  text-align: right;
}
.empty {
  margin: 40px auto;
  text-align: center;
}
.code :deep(textarea) {
  font-family: var(--sd-mono);
  font-size: 12px;
}
.foot {
  display: flex;
  gap: 8px;
  justify-content: flex-end;
}
</style>
