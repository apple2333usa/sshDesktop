<script setup lang="ts">
// 开始菜单：已固定、全部应用、搜索（应用 + 设置项 + 文件）；底部是当前用户和电源。
import { computed, nextTick, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { NDropdown, NInput, useMessage } from 'naive-ui'
import { useSession } from '@/stores/session'
import { useDesktop } from '@/stores/desktop'
import { useHost } from '@/stores/host'
import { useLogout, useSwitchShell, useVisibleApps } from '@/shells/common'
import { withDanger } from '@/components/danger'
import { api } from '@/api/http'
import { errorText } from '@/i18n'
import { filesApi } from '@/api/files'
import type { FileEntry } from '@/api/types'
import { fileKind } from '@/components/icons'
import SdIcon from '@/components/SdIcon.vue'
import { useFileOpener } from '@/apps/explorer/opener'

const props = defineProps<{ searchMode?: boolean }>()
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const session = useSession()
const desktop = useDesktop()
const host = useHost()
const message = useMessage()
const visible = useVisibleApps()
const logout = useLogout()
const switchShell = useSwitchShell()
const opener = useFileOpener('local', (id, p) => desktop.open(id, p))

const q = ref('')
const showAll = ref(false)
const showKeys = ref(false)
const input = ref<InstanceType<typeof NInput> | null>(null)
const fileHits = ref<FileEntry[]>([])
let searchCtl: AbortController | null = null
let searchTimer: number | undefined

onMounted(async () => {
  await nextTick()
  if (props.searchMode) input.value?.focus()
})

const appList = computed(() => visible.list())
const sortedApps = computed(() => [...appList.value].sort((a, b) => t(a.title).localeCompare(t(b.title), 'zh-CN')))

// 可搜索的设置项
const settingItems = computed(() => [
  { key: 'personalize', label: t('settings.personalize'), section: 'personalize' },
  { key: 'theme', label: t('settings.theme'), section: 'personalize' },
  { key: 'wallpaper', label: t('settings.wallpaper'), section: 'personalize' },
  { key: 'password', label: t('settings.changePassword'), section: 'account' },
  ...(session.isAdmin
    ? [
        { key: 'panel', label: t('settings.panel'), section: 'panel' },
        { key: 'security', label: t('settings.security'), section: 'panel' },
      ]
    : []),
  { key: 'about', label: t('settings.about'), section: 'about' },
])

const hits = computed(() => {
  const s = q.value.trim().toLowerCase()
  if (!s) return null
  const apps = appList.value.filter((a) => t(a.title).toLowerCase().includes(s) || a.id.includes(s))
  const settings = settingItems.value.filter((x) => x.label.toLowerCase().includes(s))
  return { apps, settings }
})

function onInput(v: string) {
  q.value = v
  clearTimeout(searchTimer)
  searchCtl?.abort()
  fileHits.value = []
  if (!v.trim() || !session.has('files:view')) return
  searchTimer = window.setTimeout(async () => {
    searchCtl = new AbortController()
    try {
      const r = await filesApi.search('local', '/root', v.trim(), searchCtl.signal)
      fileHits.value = r.entries.slice(0, 8)
    } catch {
      /* 忽略搜索失败 */
    }
  }, 300)
}

function open(id: string, p?: Record<string, any>) {
  desktop.open(id, p)
  emit('close')
}

function openFile(e: FileEntry) {
  opener.open(e)
  emit('close')
}

function onEnter() {
  const h = hits.value
  if (!h) return
  if (h.apps[0]) open(h.apps[0].id)
  else if (h.settings[0]) open('settings', { section: h.settings[0].section })
  else if (fileHits.value[0]) openFile(fileHits.value[0])
}

const userMenu = computed(() => [
  { key: 'settings', label: t('start.accountSettings') },
  { key: 'admin', label: t('start.switchAdmin') },
  { type: 'divider', key: 'd' },
  { key: 'logout', label: t('start.logout') },
])

function onUser(key: string) {
  emit('close')
  if (key === 'settings') desktop.open('settings', { section: 'account' })
  else if (key === 'admin') switchShell('admin')
  else if (key === 'logout') logout()
}

const powerMenu = computed(() => [
  { key: 'reboot', label: t('start.reboot'), disabled: !session.has('system:power') },
  { key: 'poweroff', label: t('start.poweroff'), disabled: !session.has('system:power') },
])

async function onPower(key: 'reboot' | 'poweroff') {
  emit('close')
  const label = key === 'reboot' ? t('start.reboot') : t('start.poweroff')
  const name = host.info?.host.hostname ?? ''
  try {
    const ok = await withDanger(
      {
        level: 2,
        action: t('start.powerAction', { action: label }),
        impact: t('start.powerImpact'),
        confirmText: `${label} ${name}`,
      },
      (pw) => api.post(`/system/power/${key}`, {}, { confirmPassword: pw }),
    )
    if (ok !== undefined) message.success(t('start.powerSent', { action: label }))
  } catch (e) {
    message.error(errorText(e))
  }
}

const shortcuts = [
  ['Alt + E', 'shortcuts.explorer'],
  ['Alt + T', 'shortcuts.terminal'],
  ['Alt + Q', 'shortcuts.switch'],
  ['Alt + W', 'shortcuts.close'],
  ['Alt + D', 'shortcuts.desktop'],
  ['Ctrl + C / X / V', 'shortcuts.clipboard'],
  ['Delete', 'shortcuts.delete'],
  ['F2', 'shortcuts.rename'],
  ['Ctrl + S', 'shortcuts.save'],
]
</script>

<template>
  <div class="start" data-testid="start-menu" @click.stop>
    <div class="search">
      <NInput
        ref="input"
        :value="q"
        round
        clearable
        :placeholder="t('start.searchPlaceholder')"
        data-testid="start-search"
        @update:value="onInput"
        @keyup.enter="onEnter"
      />
    </div>

    <div class="body sd-scroll">
      <template v-if="hits">
        <div v-if="hits.apps.length" class="sec">{{ t('start.apps') }}</div>
        <div v-for="a in hits.apps" :key="a.id" class="row" @click="open(a.id)">
          <SdIcon :name="a.icon" :size="24" /> <span>{{ t(a.title) }}</span>
        </div>
        <div v-if="hits.settings.length" class="sec">{{ t('start.settings') }}</div>
        <div v-for="s in hits.settings" :key="s.key" class="row" @click="open('settings', { section: s.section })">
          <SdIcon name="settings" :size="24" /> <span>{{ s.label }}</span>
        </div>
        <div v-if="fileHits.length" class="sec">{{ t('start.files') }}</div>
        <div v-for="f in fileHits" :key="f.path" class="row" @click="openFile(f)">
          <SdIcon :name="fileKind(f.name, f.isDir)" :size="24" />
          <div class="file">
            <div class="sd-ellipsis">{{ f.name }}</div>
            <div class="sd-muted sd-ellipsis small">{{ f.path }}</div>
          </div>
        </div>
        <div v-if="!hits.apps.length && !hits.settings.length && !fileHits.length" class="empty sd-muted">{{ t('start.noResult') }}</div>
      </template>

      <template v-else-if="showAll">
        <div class="head">
          <span class="h">{{ t('start.allApps') }}</span>
          <button class="link" @click="showAll = false">‹ {{ t('start.back') }}</button>
        </div>
        <div v-for="a in sortedApps" :key="a.id" class="row" :data-testid="'start-all-app-' + a.id" @click="open(a.id)">
          <SdIcon :name="a.icon" :size="24" /> <span>{{ t(a.title) }}</span>
        </div>
      </template>

      <template v-else-if="showKeys">
        <div class="head">
          <span class="h">{{ t('start.shortcuts') }}</span>
          <button class="link" @click="showKeys = false">‹ {{ t('start.back') }}</button>
        </div>
        <div class="keys">
          <template v-for="[k, d] in shortcuts" :key="k">
            <kbd>{{ k }}</kbd><span>{{ t(d) }}</span>
          </template>
        </div>
        <p class="sd-muted small">{{ t('start.shortcutsNote') }}</p>
      </template>

      <template v-else>
        <div class="head">
          <span class="h">{{ t('start.pinned') }}</span>
          <button class="link" data-testid="start-all" @click="showAll = true">{{ t('start.allApps') }} ›</button>
        </div>
        <div class="grid">
          <div v-for="a in appList" :key="a.id" class="tile" :data-testid="'start-app-' + a.id" @click="open(a.id)">
            <SdIcon :name="a.icon" :size="32" />
            <span class="sd-ellipsis">{{ t(a.title) }}</span>
          </div>
        </div>
        <div class="head">
          <span class="h">{{ t('start.recommended') }}</span>
        </div>
        <div class="rec">
          <div class="row" @click="open('explorer', { path: 'computer:' })"><SdIcon name="thispc" :size="28" /><span>{{ t('explorer.thisPC') }}</span></div>
          <div class="row" @click="open('explorer', { path: 'trash:' })"><SdIcon name="trash" :size="28" /><span>{{ t('explorer.trash') }}</span></div>
          <div class="row" @click="showKeys = true"><SdIcon name="about" :size="28" /><span>{{ t('start.shortcuts') }}</span></div>
        </div>
      </template>
    </div>

    <div class="foot">
      <NDropdown :options="userMenu" placement="top-start" @select="onUser">
        <button class="user" data-testid="start-user">
          <span class="avatar">{{ session.username.slice(0, 1).toUpperCase() }}</span>
          <span>{{ session.username }}</span>
        </button>
      </NDropdown>
      <NDropdown :options="powerMenu" placement="top-end" @select="onPower">
        <button class="power" :title="t('start.power')" data-testid="start-power">
          <svg viewBox="0 0 24 24"><path d="M12 3v8" stroke="currentColor" stroke-width="2" stroke-linecap="round" /><path d="M6.3 6.8a8 8 0 1 0 11.4 0" stroke="currentColor" stroke-width="2" fill="none" stroke-linecap="round" /></svg>
        </button>
      </NDropdown>
    </div>
  </div>
</template>

<style scoped>
.start {
  position: absolute;
  left: 50%;
  bottom: 60px;
  transform: translateX(-50%);
  width: min(640px, calc(100vw - 24px));
  height: min(700px, calc(100vh - 80px));
  display: flex;
  flex-direction: column;
  background: var(--sd-acrylic);
  backdrop-filter: blur(40px) saturate(150%);
  border: 1px solid var(--sd-border-strong);
  border-radius: 8px;
  box-shadow: var(--sd-shadow);
  z-index: 100001;
  animation: up 0.18s ease-out;
}
@keyframes up {
  from {
    transform: translate(-50%, 24px);
    opacity: 0;
  }
}
.search {
  padding: 24px 32px 12px;
}
.body {
  flex: 1;
  padding: 0 32px 12px;
}
.head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin: 12px 0 8px;
}
.h {
  font-weight: 600;
}
.link {
  border: none;
  background: var(--sd-hover);
  color: var(--sd-text);
  border-radius: 4px;
  padding: 3px 10px;
  font-size: 12px;
}
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(88px, 1fr));
  gap: 4px;
}
.tile {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 6px;
  padding: 12px 4px;
  border-radius: 4px;
  font-size: 12px;
  text-align: center;
}
.tile span {
  max-width: 100%;
}
.tile:hover,
.row:hover {
  background: var(--sd-hover);
}
.row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 8px 10px;
  border-radius: 4px;
}
.rec {
  display: grid;
  grid-template-columns: 1fr 1fr;
}
.sec {
  font-weight: 600;
  margin: 10px 0 4px;
}
.file {
  min-width: 0;
}
.small {
  font-size: 12px;
}
.empty {
  padding: 24px 0;
  text-align: center;
}
.keys {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 8px 16px;
  align-items: center;
}
kbd {
  font-family: var(--sd-mono);
  font-size: 12px;
  padding: 2px 8px;
  border-radius: 4px;
  border: 1px solid var(--sd-border-strong);
  background: var(--sd-surface);
  white-space: nowrap;
}
.foot {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 12px 40px;
  border-top: 1px solid var(--sd-border);
  background: var(--sd-hover);
  border-radius: 0 0 8px 8px;
}
.user,
.power {
  border: none;
  background: transparent;
  color: var(--sd-text);
  border-radius: 4px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 6px 10px;
}
.user:hover,
.power:hover {
  background: var(--sd-hover);
}
.avatar {
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: var(--sd-accent);
  color: #fff;
  display: flex;
  align-items: center;
  justify-content: center;
  font-weight: 600;
}
.power svg {
  width: 20px;
  height: 20px;
}
</style>
