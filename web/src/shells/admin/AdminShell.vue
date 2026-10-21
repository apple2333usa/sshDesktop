<script setup lang="ts">
// 后台外观：侧边栏 + 页面布局，适合快速操作和手机访问。
import { computed, h, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useI18n } from 'vue-i18n'
import {
  NBadge, NButton, NDrawer, NDrawerContent, NDropdown, NLayout, NLayoutContent, NLayoutHeader, NLayoutSider, NMenu,
  NPopover, NProgress, type MenuOption,
} from 'naive-ui'
import { useSession } from '@/stores/session'
import { usePrefs, isCompactDevice } from '@/stores/prefs'
import { useNotify } from '@/stores/notify'
import { useHost } from '@/stores/host'
import { appMap } from '@/apps/registry'
import { useLogout, useShellBoot, useSwitchShell, useVisibleApps } from '@/shells/common'
import AppContainer from '@/apps/AppContainer.vue'
import SdIcon from '@/components/SdIcon.vue'
import FileDialogs from '@/apps/explorer/FileDialogs.vue'
import { formatTime } from '@/i18n'

useShellBoot((id) => go(id))
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSession()
const prefs = usePrefs()
const notify = useNotify()
const host = useHost()
const visible = useVisibleApps()
const logout = useLogout()
const switchShell = useSwitchShell()

const compact = ref(isCompactDevice())
const narrow = ref(window.innerWidth < 768)
const drawer = ref(false)
const title = ref('')
// 每个页面的内部状态（例如资源管理器当前目录），切换页面后回来仍保留
const pageState = reactive<Record<string, Record<string, any>>>({})

function onResize() {
  compact.value = isCompactDevice()
  narrow.value = window.innerWidth < 768
}
onMounted(() => window.addEventListener('resize', onResize))
onUnmounted(() => window.removeEventListener('resize', onResize))

const list = computed(() => visible.list())
const appId = computed(() => {
  const id = route.params.app as string | undefined
  if (id && appMap[id] && visible.can(appMap[id])) return id
  return list.value[0]?.id ?? 'settings'
})

const pageKey = computed(() => `${appId.value}|${route.query.p ?? ''}`)
const appProps = computed(() => {
  const key = pageKey.value
  if (!pageState[key]) {
    let p: Record<string, any> = {}
    try {
      p = route.query.p ? JSON.parse(route.query.p as string) : {}
    } catch {
      /* 忽略 */
    }
    pageState[key] = p
  }
  return pageState[key]
})

watch(appId, () => {
  title.value = ''
  drawer.value = false
})

const icon = (name: string) => () => h(SdIcon, { name, size: 20 })
const menuOptions = computed<MenuOption[]>(() => {
  const groups: [string, string][] = [
    ['system', t('admin.groupSystem')],
    ['manage', t('admin.groupManage')],
  ]
  return groups.map(([g, label]) => ({
    type: 'group',
    key: g,
    label,
    children: list.value.filter((a) => a.group === g).map((a) => ({ key: a.id, label: t(a.title), icon: icon(a.icon) })),
  }))
})

function go(id: string, props?: Record<string, any>) {
  router.push({ name: 'admin', params: { app: id }, query: props && Object.keys(props).length ? { p: JSON.stringify(props) } : {} })
}

const userOptions = computed(() => [
  ...(compact.value ? [] : [{ key: 'desktop', label: t('admin.switchDesktop') }]),
  { key: 'settings', label: t('start.accountSettings') },
  { type: 'divider', key: 'd' },
  { key: 'logout', label: t('start.logout') },
])

function onUser(k: string) {
  if (k === 'desktop') switchShell('desktop')
  else if (k === 'settings') go('settings', { section: 'account' })
  else if (k === 'logout') logout()
}

function toggleTheme() {
  prefs.p.theme = prefs.dark ? 'light' : 'dark'
}

const pageTitle = computed(() => {
  const base = t(appMap[appId.value]?.title ?? '')
  return title.value ? `${title.value} - ${base}` : base
})
</script>

<template>
  <NLayout class="admin" has-sider position="absolute">
    <NLayoutSider v-if="!narrow" bordered collapse-mode="width" :collapsed-width="64" :width="220" show-trigger="bar" :native-scrollbar="false">
      <div class="brand">
        <img src="/favicon.svg" alt="" />
        <span>sshDesktop</span>
      </div>
      <NMenu :value="appId" :options="menuOptions" :collapsed-width="64" :indent="20" data-testid="admin-menu" @update:value="(k: string) => go(k)" />
    </NLayoutSider>
    <NDrawer v-else v-model:show="drawer" placement="left" :width="240">
      <NDrawerContent :body-content-style="{ padding: '0' }">
        <div class="brand"><img src="/favicon.svg" alt="" /><span>sshDesktop</span></div>
        <NMenu :value="appId" :options="menuOptions" @update:value="(k: string) => go(k)" />
      </NDrawerContent>
    </NDrawer>
    <NLayout class="main">
      <NLayoutHeader bordered class="header">
        <NButton v-if="narrow" quaternary size="small" data-testid="admin-drawer" @click="drawer = true">☰</NButton>
        <div class="title sd-ellipsis">{{ pageTitle }}</div>
        <span class="host sd-muted sd-ellipsis">{{ host.label }}</span>
        <NPopover trigger="click" placement="bottom-end" :width="320" @update:show="(v: boolean) => v && notify.markAllRead()">
          <template #trigger>
            <NBadge :value="notify.unread" :max="9" :show="notify.unread > 0">
              <NButton quaternary circle size="small" :title="t('notify.title')">🔔</NButton>
            </NBadge>
          </template>
          <div class="nlist">
            <div v-if="!notify.list.length" class="sd-muted">{{ t('notify.empty') }}</div>
            <div v-for="n in notify.list.slice(0, 20)" :key="n.id" class="nitem">
              <div class="nt">{{ n.title }} <span class="sd-muted">{{ formatTime(n.time).slice(11) }}</span></div>
              <div v-if="n.content" class="sd-muted small">{{ n.content }}</div>
              <NProgress v-if="n.progress !== undefined && !n.done" :percentage="Math.round(n.progress)" :show-indicator="false" :height="4" />
            </div>
          </div>
        </NPopover>
        <NButton quaternary circle size="small" :title="t('admin.toggleTheme')" @click="toggleTheme">{{ prefs.dark ? '☀' : '☾' }}</NButton>
        <NDropdown :options="userOptions" placement="bottom-end" @select="onUser">
          <NButton quaternary size="small" data-testid="admin-user">{{ session.username }} ▾</NButton>
        </NDropdown>
      </NLayoutHeader>
      <NLayoutContent class="content" content-style="height: 100%">
        <AppContainer
          :key="pageKey"
          :app-id="appId"
          :app-props="appProps"
          mode="admin"
          :window-id="null"
          :active="true"
          :visible="true"
          @title="(v) => (title = v)"
          @close="router.back()"
          @open="go"
          @guard="() => {}"
        />
      </NLayoutContent>
    </NLayout>
    <FileDialogs />
  </NLayout>
</template>

<style scoped>
.admin {
  height: 100%;
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 56px;
  padding: 0 20px;
  font-weight: 600;
  font-size: 16px;
  white-space: nowrap;
  overflow: hidden;
}
.brand img {
  width: 26px;
  height: 26px;
}
.main {
  display: flex;
  flex-direction: column;
}
.header {
  height: 56px;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 0 16px;
  flex-shrink: 0;
}
.title {
  font-weight: 600;
  font-size: 16px;
  min-width: 0;
}
.host {
  margin-left: auto;
  font-size: 12px;
  max-width: 30vw;
}
.content {
  position: absolute;
  top: 56px;
  bottom: 0;
  left: 0;
  right: 0;
}
.nlist {
  max-height: 60vh;
  overflow: auto;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.nt {
  font-weight: 600;
  font-size: 13px;
}
.small {
  font-size: 12px;
  word-break: break-all;
}
</style>
