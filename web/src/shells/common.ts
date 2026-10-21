// 两种外观共用的启动逻辑：读取桌面状态、本机信息，开始接收任务通知。
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { loadDesktopState, save, stopPersist } from '@/stores/persist'
import { useHost } from '@/stores/host'
import { useNotify } from '@/stores/notify'
import { useNodes } from '@/stores/nodes'
import { useSession } from '@/stores/session'
import { useDesktop } from '@/stores/desktop'
import { usePrefs } from '@/stores/prefs'
import { apps, type AppManifest } from '@/apps/registry'
import { api } from '@/api/http'
import type { UpgradeStatus } from '@/api/types'
import { i18n } from '@/i18n'

let booted = false

export function useShellBoot(openApp: (id: string) => void) {
  const host = useHost()
  const notify = useNotify()
  const nodes = useNodes()
  const session = useSession()
  onMounted(async () => {
    notify.start()
    if (!booted) {
      booted = true
      await Promise.all([loadDesktopState(), host.load(), nodes.start()])
      // 管理员第一次进入时还没有体检报告：打开环境体检，让管理员先了解服务器的现状
      if (session.isAdmin) {
        try {
          if (!(await api.get('/nodes/local/inspect'))) openApp('inspect')
        } catch {
          /* 忽略 */
        }
        notifyUpgrade()
      }
    }
  })
}

/** 有新版本时提醒管理员一次（每个版本每个浏览器只提醒一次）。 */
async function notifyUpgrade() {
  const notify = useNotify()
  try {
    const st = await api.get<UpgradeStatus>('/system/upgrade')
    const tag = st.latest?.tag
    if (!st.available || !tag) return
    const key = 'sd-upgrade-notified'
    if (localStorage.getItem(key) === tag) return
    localStorage.setItem(key, tag)
    notify.push({ type: 'info', title: i18n.global.t('upgrade.notice', { v: tag }), content: i18n.global.t('upgrade.noticeBody') })
  } catch {
    /* 查询不到最新版本时不提醒 */
  }
}

/** 当前账号可用的应用（按权限过滤）。 */
export function useVisibleApps() {
  const session = useSession()
  return {
    list: (includeHidden = false): AppManifest[] =>
      apps.filter((a) => (includeHidden || !a.hidden) && (a.adminOnly ? session.isAdmin : session.has(a.permission))),
    can: (a: AppManifest | undefined) => !!a && (a.adminOnly ? session.isAdmin : session.has(a.permission)),
  }
}

export function useLogout() {
  const session = useSession()
  const router = useRouter()
  const notify = useNotify()
  const nodes = useNodes()
  const desktop = useDesktop()
  return async () => {
    await save()
    stopPersist()
    booted = false
    notify.stop()
    nodes.stop()
    desktop.reset()
    await session.logout()
    router.replace({ name: 'login' })
  }
}

export function useSwitchShell() {
  const prefs = usePrefs()
  const router = useRouter()
  return (to: 'desktop' | 'admin') => {
    prefs.p.appearance = to
    router.push({ name: to })
  }
}

