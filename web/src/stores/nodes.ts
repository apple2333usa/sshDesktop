// 节点列表（本机 + 远程服务器）与连接状态。登录后载入，经 node-status 频道实时更新，
// 窗口标题的节点标签、任务栏托盘、资源管理器的“网络”位置都从这里取。
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/http'
import { realtime } from '@/api/realtime'
import type { NodeStatus, NodeView } from '@/api/types'
import { useNotify } from './notify'

export const LOCAL = 'local'

/** 没有设置颜色的远程节点按 ID 取一个固定的颜色。 */
const palette = ['#8764b8', '#0078d4', '#038387', '#ca5010', '#498205', '#c239b3', '#986f0b', '#e74856']

export function nodeColor(n: Pick<NodeView, 'id' | 'color'> | undefined): string {
  if (!n) return palette[0]
  if (n.color) return n.color
  let h = 0
  for (const c of n.id) h = (h * 31 + c.charCodeAt(0)) >>> 0
  return palette[h % palette.length]
}

export const useNodes = defineStore('nodes', () => {
  const list = ref<NodeView[]>([])
  const loaded = ref(false)
  let unsub: (() => void) | null = null

  const map = computed(() => Object.fromEntries(list.value.map((n) => [n.id, n])) as Record<string, NodeView>)
  const remote = computed(() => list.value.filter((n) => n.kind === 'ssh'))
  const onlineCount = computed(() => list.value.filter((n) => n.status.state === 'online').length)
  const problems = computed(() => remote.value.filter((n) => n.status.state !== 'online' && n.status.state !== 'connecting'))

  async function load() {
    try {
      list.value = await api.get<NodeView[]>('/nodes')
      loaded.value = true
    } catch {
      /* 忽略 */
    }
  }

  function onStatus(ev: { id: string; status?: NodeStatus; removed?: boolean }) {
    if (ev.removed) {
      list.value = list.value.filter((n) => n.id !== ev.id)
      return
    }
    const n = map.value[ev.id]
    if (!n || !ev.status) {
      // 新节点（可能是别的会话添加的）
      if (!n) load()
      return
    }
    const prev = n.status.state
    n.status = ev.status
    const s = ev.status.state
    if (prev === 'online' && s !== 'online' && s !== 'connecting') {
      useNotify().push({ type: 'warning', title: `${n.name} 已断开`, content: ev.status.error || '正在重连' })
    } else if (prev !== 'online' && prev !== 'connecting' && s === 'online') {
      useNotify().push({ type: 'success', title: `${n.name} 已恢复连接` })
    }
    if (s === 'hostkey-mismatch' && prev !== s) {
      useNotify().push({ type: 'error', title: `${n.name} 的主机密钥已变化`, content: '已拒绝连接。请在“服务器”中核实后重新确认指纹。' })
    }
  }

  function start() {
    if (!unsub) unsub = realtime.subscribe('node-status', onStatus)
    return load()
  }

  function stop() {
    unsub?.()
    unsub = null
    list.value = []
    loaded.value = false
  }

  /** 显示用名称；未知节点返回 ID。 */
  function name(id: string): string {
    return map.value[id]?.name ?? (id === LOCAL ? '本机' : id)
  }

  /** 确认框中的目标节点描述：名称（用户@地址）。 */
  function label(id: string): string {
    const n = map.value[id]
    if (!n) return name(id)
    return n.kind === 'ssh' ? `${n.name}（${n.username}@${n.host}:${n.port}）` : n.name
  }

  function color(id: string): string {
    return nodeColor(map.value[id] ?? { id, color: '' })
  }

  function online(id: string): boolean {
    return id === LOCAL || map.value[id]?.status.state === 'online'
  }

  function upsert(v: NodeView) {
    const i = list.value.findIndex((n) => n.id === v.id)
    if (i >= 0) list.value[i] = v
    else list.value.push(v)
  }

  return { list, loaded, map, remote, onlineCount, problems, load, start, stop, name, label, color, online, upsert }
})
