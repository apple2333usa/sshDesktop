import { createRouter, createWebHistory } from 'vue-router'
import { useSession } from '@/stores/session'
import { usePrefs, isCompactDevice } from '@/stores/prefs'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('@/views/LoginView.vue'), meta: { public: true } },
    { path: '/desktop', name: 'desktop', component: () => import('@/shells/desktop/DesktopShell.vue') },
    { path: '/admin/:app?', name: 'admin', component: () => import('@/shells/admin/AdminShell.vue') },
    { path: '/:pathMatch(.*)*', name: 'home', component: { render: () => null } },
  ],
})

/** 根据设备和偏好决定使用哪种外观。手机和小屏幕始终使用后台外观。 */
export function preferredShell(): 'desktop' | 'admin' {
  if (isCompactDevice()) return 'admin'
  const prefs = usePrefs()
  const session = useSession()
  const pref = prefs.p.appearance !== 'auto' ? prefs.p.appearance : (session.me?.defaultAppearance ?? 'auto')
  return pref === 'admin' ? 'admin' : 'desktop'
}

router.beforeEach(async (to) => {
  const session = useSession()
  if (!session.loaded) await session.load()
  if (to.meta.public) {
    if (session.me && to.name === 'login') return { name: preferredShell() }
    return true
  }
  if (!session.me) return { name: 'login', query: to.fullPath !== '/' ? { redirect: to.fullPath } : {} }
  if (to.name === 'home') return { name: preferredShell() }
  if (to.name === 'desktop' && isCompactDevice()) return { name: 'admin' }
  return true
})
