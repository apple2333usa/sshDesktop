<script setup lang="ts">
// 设置：个性化（主题、壁纸、外观、终端）、账号（修改密码）、面板（仅管理员）、关于。
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import {
  NButton, NDynamicInput, NForm, NFormItem, NInput, NInputNumber, NRadioButton, NRadioGroup, NSelect, NSwitch, useMessage,
} from 'naive-ui'
import { api } from '@/api/http'
import type { RuntimeSettings, SystemInfo } from '@/api/types'
import { useAppHost } from '@/apps/host'
import { usePrefs, type Snippet } from '@/stores/prefs'
import { useSession } from '@/stores/session'
import { wallpapers, wallpaperCSS } from '@/theme/wallpapers'
import { errorText, formatDuration, formatTime } from '@/i18n'
import SdIcon from '@/components/SdIcon.vue'
import PanelAccess from './PanelAccess.vue'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const prefs = usePrefs()
const session = useSession()

type Section = 'personalize' | 'terminal' | 'account' | 'panel' | 'about'
const section = ref<Section>((host.props.section as Section) || 'personalize')
watch(section, (v) => host.setState({ section: v }))

const sections = computed(() =>
  [
    { id: 'personalize', label: t('settings.personalize'), icon: 'settings' },
    { id: 'terminal', label: t('settings.terminal'), icon: 'terminal' },
    { id: 'account', label: t('settings.account'), icon: 'users' },
    ...(session.isAdmin ? [{ id: 'panel', label: t('settings.panel'), icon: 'services' }] : []),
    { id: 'about', label: t('settings.about'), icon: 'about' },
  ] as { id: Section; label: string; icon: string }[],
)

/* ---------- 账号 ---------- */
const pw = reactive({ old: '', next: '', confirm: '', saving: false })
async function changePassword() {
  if (pw.next !== pw.confirm) {
    message.error(t('settings.passwordMismatch'))
    return
  }
  pw.saving = true
  try {
    await api.post('/auth/password', { oldPassword: pw.old, newPassword: pw.next })
    message.success(t('settings.passwordChanged'))
    Object.assign(pw, { old: '', next: '', confirm: '' })
  } catch (e) {
    message.error(errorText(e))
  } finally {
    pw.saving = false
  }
}

/* ---------- 面板 ---------- */
const rt = ref<RuntimeSettings | null>(null)
const savingRt = ref(false)
async function loadRuntime() {
  if (!session.isAdmin) return
  try {
    rt.value = await api.get<RuntimeSettings>('/settings')
  } catch (e) {
    message.error(errorText(e))
  }
}
async function saveRuntime() {
  if (!rt.value) return
  savingRt.value = true
  try {
    rt.value = await api.put<RuntimeSettings>('/settings', rt.value)
    message.success(t('common.saved'))
  } catch (e) {
    message.error(errorText(e))
  } finally {
    savingRt.value = false
  }
}

/* ---------- 关于 ---------- */
const sys = ref<SystemInfo | null>(null)
async function loadSystem() {
  try {
    sys.value = await api.get<SystemInfo>('/system/info')
  } catch (e) {
    message.error(errorText(e))
  }
}

onMounted(() => {
  loadRuntime()
  loadSystem()
})

const appearanceOptions = computed(() => [
  { label: t('settings.appearanceAuto'), value: 'auto' },
  { label: t('settings.appearanceDesktop'), value: 'desktop' },
  { label: t('settings.appearanceAdmin'), value: 'admin' },
])

const snippets = computed({
  get: () => prefs.p.terminal.snippets,
  set: (v: Snippet[]) => (prefs.p.terminal.snippets = v),
})
</script>

<template>
  <div class="settings" data-testid="settings">
    <nav class="nav">
      <div class="me">
        <div class="avatar">{{ session.username.slice(0, 1).toUpperCase() }}</div>
        <div>
          <div class="name">{{ session.username }}</div>
          <div class="sd-muted small">{{ session.me?.user.roleName }}</div>
        </div>
      </div>
      <div
        v-for="s in sections"
        :key="s.id"
        class="item"
        :class="{ active: section === s.id }"
        :data-testid="'settings-' + s.id"
        @click="section = s.id"
      >
        <SdIcon :name="s.icon" :size="18" /><span>{{ s.label }}</span>
      </div>
    </nav>

    <main class="content sd-scroll">
      <!-- 个性化 -->
      <template v-if="section === 'personalize'">
        <h2>{{ t('settings.personalize') }}</h2>
        <div class="card">
          <div class="row">
            <div>
              <div class="title">{{ t('settings.theme') }}</div>
              <div class="sd-muted small">{{ t('settings.themeHint') }}</div>
            </div>
            <NRadioGroup v-model:value="prefs.p.theme" size="small">
              <NRadioButton value="auto">{{ t('settings.themeAuto') }}</NRadioButton>
              <NRadioButton value="light">{{ t('settings.themeLight') }}</NRadioButton>
              <NRadioButton value="dark" data-testid="theme-dark">{{ t('settings.themeDark') }}</NRadioButton>
            </NRadioGroup>
          </div>
        </div>
        <div class="card">
          <div class="title">{{ t('settings.wallpaper') }}</div>
          <div class="walls">
            <div
              v-for="w in wallpapers"
              :key="w.id"
              class="wall"
              :class="{ active: prefs.p.wallpaper === w.id }"
              :style="{ backgroundImage: wallpaperCSS(w.id, prefs.dark) }"
              :title="w.name"
              @click="prefs.p.wallpaper = w.id"
            />
          </div>
        </div>
        <div class="card">
          <div class="row">
            <div>
              <div class="title">{{ t('settings.appearance') }}</div>
              <div class="sd-muted small">{{ t('settings.appearanceHint') }}</div>
            </div>
            <NSelect v-model:value="prefs.p.appearance" :options="appearanceOptions" size="small" style="width: 180px" />
          </div>
          <div class="row">
            <div>
              <div class="title">{{ t('settings.desktopDir') }}</div>
              <div class="sd-muted small">{{ t('settings.desktopDirHint') }}</div>
            </div>
            <NInput v-model:value="prefs.p.desktopDir" size="small" style="width: 220px" class="sd-mono" />
          </div>
          <div class="row">
            <div class="title">{{ t('settings.showHidden') }}</div>
            <NSwitch v-model:value="prefs.p.explorer.showHidden" />
          </div>
        </div>
      </template>

      <!-- 终端 -->
      <template v-else-if="section === 'terminal'">
        <h2>{{ t('settings.terminal') }}</h2>
        <div class="card">
          <div class="row">
            <div class="title">{{ t('settings.fontSize') }}</div>
            <NInputNumber v-model:value="prefs.p.terminal.fontSize" :min="10" :max="28" size="small" style="width: 120px" />
          </div>
          <div class="row">
            <div class="title">{{ t('settings.scheme') }}</div>
            <NRadioGroup v-model:value="prefs.p.terminal.scheme" size="small">
              <NRadioButton value="dark">{{ t('settings.themeDark') }}</NRadioButton>
              <NRadioButton value="light">{{ t('settings.themeLight') }}</NRadioButton>
            </NRadioGroup>
          </div>
        </div>
        <div class="card">
          <div class="title">{{ t('settings.snippets') }}</div>
          <div class="sd-muted small" style="margin-bottom: 8px">{{ t('settings.snippetsHint') }}</div>
          <NDynamicInput v-model:value="snippets" :on-create="() => ({ name: '', command: '' })">
            <template #default="{ value }">
              <div class="snippet">
                <NInput v-model:value="value.name" size="small" :placeholder="t('settings.snippetName')" style="width: 140px" />
                <NInput v-model:value="value.command" size="small" class="sd-mono" :placeholder="t('settings.snippetCommand')" />
              </div>
            </template>
          </NDynamicInput>
        </div>
      </template>

      <!-- 账号 -->
      <template v-else-if="section === 'account'">
        <h2>{{ t('settings.account') }}</h2>
        <div class="card">
          <dl class="kv">
            <dt>{{ t('users.username') }}</dt>
            <dd>{{ session.username }}</dd>
            <dt>{{ t('users.role') }}</dt>
            <dd>{{ session.me?.user.roleName }}</dd>
            <dt>{{ t('users.lastLogin') }}</dt>
            <dd>{{ formatTime(session.me?.user.lastLoginAt) }} {{ session.me?.user.lastLoginIp }}</dd>
          </dl>
        </div>
        <div class="card">
          <div class="title">{{ t('settings.changePassword') }}</div>
          <NForm label-placement="left" label-width="90" style="max-width: 420px; margin-top: 10px">
            <NFormItem :label="t('settings.oldPassword')">
              <NInput v-model:value="pw.old" type="password" show-password-on="click" data-testid="pw-old" />
            </NFormItem>
            <NFormItem :label="t('settings.newPassword')">
              <NInput v-model:value="pw.next" type="password" show-password-on="click" data-testid="pw-new" />
            </NFormItem>
            <NFormItem :label="t('settings.confirmPassword')">
              <NInput v-model:value="pw.confirm" type="password" show-password-on="click" data-testid="pw-confirm" />
            </NFormItem>
            <NButton type="primary" :loading="pw.saving" :disabled="!pw.old || !pw.next" data-testid="pw-save" @click="changePassword">
              {{ t('settings.changePassword') }}
            </NButton>
            <div class="sd-muted small" style="margin-top: 8px">{{ t('settings.passwordHint') }}</div>
          </NForm>
        </div>
      </template>

      <!-- 面板 -->
      <template v-else-if="section === 'panel' && rt">
        <h2>{{ t('settings.panel') }}</h2>
        <div class="card">
          <div class="title">{{ t('settings.security') }}</div>
          <div class="row">
            <span>{{ t('settings.sessionTimeout') }}</span>
            <NInputNumber v-model:value="rt.sessionTimeoutHours" :min="1" :max="720" size="small" style="width: 140px"><template #suffix>{{ t('settings.hours') }}</template></NInputNumber>
          </div>
          <div class="row">
            <span>{{ t('settings.banMaxFailures') }}</span>
            <NInputNumber v-model:value="rt.banMaxFailures" :min="1" :max="100" size="small" style="width: 140px" />
          </div>
          <div class="row">
            <span>{{ t('settings.banWindow') }}</span>
            <NInputNumber v-model:value="rt.banWindowMinutes" :min="1" :max="1440" size="small" style="width: 140px"><template #suffix>{{ t('settings.minutes') }}</template></NInputNumber>
          </div>
          <div class="row">
            <span>{{ t('settings.banMinutes') }}</span>
            <NInputNumber v-model:value="rt.banMinutes" :min="1" :max="525600" size="small" style="width: 140px"><template #suffix>{{ t('settings.minutes') }}</template></NInputNumber>
          </div>
          <div class="row">
            <div>
              <div>{{ t('settings.trustedProxy') }}</div>
              <div class="sd-muted small">{{ t('settings.trustedProxyHint') }}</div>
            </div>
            <NSwitch v-model:value="rt.trustedProxy" />
          </div>
        </div>
        <div class="card">
          <div class="title">{{ t('settings.general') }}</div>
          <div class="row">
            <span>{{ t('settings.defaultAppearance') }}</span>
            <NSelect v-model:value="rt.defaultAppearance" :options="appearanceOptions" size="small" style="width: 180px" />
          </div>
          <div class="row">
            <span>{{ t('settings.terminalKeep') }}</span>
            <NInputNumber v-model:value="rt.terminalKeepMinutes" :min="1" :max="1440" size="small" style="width: 140px"><template #suffix>{{ t('settings.minutes') }}</template></NInputNumber>
          </div>
          <div class="row">
            <span>{{ t('settings.trashRetention') }}</span>
            <NInputNumber v-model:value="rt.trashRetentionDays" :min="1" :max="3650" size="small" style="width: 140px"><template #suffix>{{ t('settings.days') }}</template></NInputNumber>
          </div>
          <div class="row">
            <span>{{ t('settings.auditRetention') }}</span>
            <NInputNumber v-model:value="rt.auditRetentionDays" :min="30" :max="3650" size="small" style="width: 140px"><template #suffix>{{ t('settings.days') }}</template></NInputNumber>
          </div>
        </div>
        <NButton type="primary" :loading="savingRt" data-testid="settings-save" @click="saveRuntime">{{ t('common.save') }}</NButton>
        <PanelAccess />
      </template>

      <!-- 关于 -->
      <template v-else-if="section === 'about'">
        <h2>{{ t('settings.about') }}</h2>
        <div class="card about">
          <img src="/favicon.svg" alt="" width="48" height="48" />
          <div>
            <div class="title">sshDesktop {{ session.me?.version }}</div>
            <div class="sd-muted small">{{ t('settings.aboutText') }}</div>
            <a class="small" href="https://github.com/apple2333usa/sshDesktop" target="_blank" rel="noopener">github.com/apple2333usa/sshDesktop</a>
            <span class="sd-muted small"> · GPL-3.0</span>
          </div>
        </div>
        <div v-if="sys" class="card">
          <dl class="kv">
            <dt>{{ t('settings.hostname') }}</dt>
            <dd>{{ sys.host.hostname }}</dd>
            <dt>{{ t('settings.os') }}</dt>
            <dd>{{ sys.host.platform }} {{ sys.host.platformVersion }} ({{ sys.host.arch }})</dd>
            <dt>{{ t('settings.kernel') }}</dt>
            <dd>{{ sys.host.kernel }}</dd>
            <dt>CPU</dt>
            <dd>{{ sys.host.cpuModel }} × {{ sys.host.cpuThreads }}</dd>
            <dt>{{ t('monitor.uptime') }}</dt>
            <dd>{{ formatDuration(sys.uptime) }}</dd>
            <dt>{{ t('settings.addresses') }}</dt>
            <dd class="sd-mono">{{ (sys.addrs ?? []).join('　') }}</dd>
            <template v-if="session.isAdmin">
              <dt>{{ t('settings.listen') }}</dt>
              <dd class="sd-mono">{{ sys.panel.listen }}{{ sys.panel.https ? ' (HTTPS)' : '' }}{{ sys.panel.domain ? ' · ' + sys.panel.domain : '' }}</dd>
              <dt>{{ t('settings.dataDir') }}</dt>
              <dd class="sd-mono">{{ sys.panel.dataDir }}</dd>
            </template>
          </dl>
        </div>
        <div v-if="session.isAdmin" class="card">
          <div class="title">{{ t('settings.upgrade') }}</div>
          <div class="sd-muted small">{{ t('settings.upgradeHint') }}</div>
          <pre class="cmd sd-mono">sshdesktop upgrade</pre>
        </div>
      </template>
    </main>
  </div>
</template>

<style scoped>
.settings {
  height: 100%;
  display: flex;
  background: var(--sd-surface);
}
.nav {
  width: 220px;
  flex-shrink: 0;
  padding: 12px 8px;
  border-right: 1px solid var(--sd-border);
}
.me {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 10px 16px;
}
.avatar {
  width: 40px;
  height: 40px;
  border-radius: 50%;
  background: var(--sd-accent);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 18px;
  font-weight: 600;
}
.name {
  font-weight: 600;
}
.item {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 36px;
  padding: 0 12px;
  border-radius: 4px;
  position: relative;
  cursor: default;
  font-size: 13px;
}
.item:hover {
  background: var(--sd-hover);
}
.item.active {
  background: var(--sd-active);
}
.item.active::before {
  content: '';
  position: absolute;
  left: 0;
  top: 10px;
  bottom: 10px;
  width: 3px;
  border-radius: 2px;
  background: var(--sd-accent);
}
.content {
  flex: 1;
  min-width: 0;
  padding: 8px 24px 24px;
}
h2 {
  font-size: 22px;
  font-weight: 600;
  margin: 10px 0 16px;
}
.card {
  background: var(--sd-surface-2);
  border: 1px solid var(--sd-border);
  border-radius: var(--sd-radius);
  padding: 14px 16px;
  margin-bottom: 10px;
  max-width: 820px;
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
  padding: 8px 0;
  font-size: 13px;
}
.row + .row {
  border-top: 1px solid var(--sd-border);
}
.walls {
  display: flex;
  flex-wrap: wrap;
  gap: 10px;
  margin-top: 10px;
}
.wall {
  width: 120px;
  height: 72px;
  border-radius: 6px;
  background-size: cover;
  background-position: center;
  border: 2px solid transparent;
  cursor: pointer;
}
.wall.active {
  border-color: var(--sd-accent);
}
.snippet {
  display: flex;
  gap: 6px;
  flex: 1;
}
.kv {
  display: grid;
  grid-template-columns: 110px 1fr;
  gap: 8px;
  margin: 0;
  font-size: 13px;
}
.kv dt {
  color: var(--sd-text-2);
}
.kv dd {
  margin: 0;
  word-break: break-all;
}
.about {
  display: flex;
  gap: 16px;
  align-items: center;
}
.cmd {
  margin: 8px 0 0;
  padding: 10px 12px;
  border-radius: 4px;
  background: var(--sd-bg);
  font-size: 12px;
  overflow-x: auto;
}
@media (max-width: 640px) {
  .settings {
    flex-direction: column;
  }
  .nav {
    width: auto;
    display: flex;
    overflow-x: auto;
    border-right: 0;
    border-bottom: 1px solid var(--sd-border);
    padding: 6px;
  }
  .me {
    display: none;
  }
  .item {
    flex-shrink: 0;
  }
  .content {
    padding: 8px 14px 20px;
  }
}
</style>
