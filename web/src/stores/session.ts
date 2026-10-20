import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/http'
import { realtime } from '@/api/realtime'
import type { Me } from '@/api/types'

export const useSession = defineStore('session', () => {
  const me = ref<Me | null>(null)
  const loaded = ref(false)

  const perms = computed(() => new Set(me.value?.permissions ?? []))
  const isAdmin = computed(() => !!me.value?.user.isAdmin)
  const username = computed(() => me.value?.user.username ?? '')

  function has(p: string | undefined) {
    return !p || isAdmin.value || perms.value.has(p)
  }

  async function load() {
    try {
      me.value = await api.get<Me>('/auth/me')
      realtime.start()
    } catch {
      me.value = null
    } finally {
      loaded.value = true
    }
    return me.value
  }

  async function login(username: string, password: string) {
    me.value = await api.post<Me>('/auth/login', { username, password })
    loaded.value = true
    realtime.start()
  }

  async function logout() {
    try {
      await api.post('/auth/logout')
    } finally {
      clear()
    }
  }

  function clear() {
    realtime.stop()
    me.value = null
  }

  return { me, loaded, isAdmin, username, has, load, login, logout, clear }
})
