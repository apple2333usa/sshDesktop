// 本机信息（主机名、IP 等），用于确认框标明目标节点和桌面显示。
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/http'
import type { SystemInfo } from '@/api/types'

export const useHost = defineStore('host', () => {
  const info = ref<SystemInfo | null>(null)

  async function load() {
    try {
      info.value = await api.get<SystemInfo>('/system/info')
    } catch {
      /* 忽略 */
    }
  }

  const label = computed(() => {
    const h = info.value
    if (!h) return '本机'
    const ip = h.addrs?.find((a) => !a.includes(':')) ?? h.addrs?.[0] ?? ''
    return ip ? `${h.host.hostname}（${ip}）` : h.host.hostname
  })

  return { info, label, load }
})
