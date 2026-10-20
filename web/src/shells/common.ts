// 两种外观共用的启动逻辑：读取桌面状态、本机信息，开始接收任务通知。
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { loadDesktopState, save, stopPersist } from '@/stores/persist'
import { useHost } from '@/stores/host'
import { useNotify } from '@/stores/notify'
import { useSession } from '@/stores/session'
import { useDesktop } from '@/stores/desktop'
import { usePrefs } from '@/stores/prefs'
import { apps, type AppManifest } from '@/apps/registry'

let booted = false

export function useShellBoot() {
  const host = useHost()
  const notify = useNotify()
  onMounted(async () => {
    notify.start()
    if (!booted) {
      booted = true
      await Promise.all([loadDesktopState(), host.load()])
    }
  })
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
  const desktop = useDesktop()
  return async () => {
    await save()
    stopPersist()
    booted = false
    notify.stop()
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

