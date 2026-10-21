// 通知中心：后台任务进度与结果、上传进度、各类提示。面板开着时右下角弹出提示。
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'
import { api } from '@/api/http'
import { realtime } from '@/api/realtime'
import type { ProbeEvent, Task, TaskEvent } from '@/api/types'
import { useSession } from './session'

export type NoticeType = 'info' | 'success' | 'warning' | 'error'

export interface Notice {
  id: number
  type: NoticeType
  title: string
  content?: string
  time: number
  read: boolean
  /** 进度（0~100），用于任务和上传 */
  progress?: number
  taskId?: string
  done?: boolean
  cancel?: () => void
}

let seq = 0

export const useNotify = defineStore('notify', () => {
  const list = ref<Notice[]>([])
  const toasts = ref<Notice[]>([])
  const tasks = ref<Record<string, Task>>({})
  const waiters = new Map<string, ((t: Task) => void)[]>()
  let unsub: (() => void) | null = null
  let unsubProbe: (() => void) | null = null
  let unsubNotice: (() => void) | null = null

  const unread = computed(() => list.value.filter((n) => !n.read).length)
  const running = computed(() => list.value.filter((n) => n.progress !== undefined && !n.done))

  function push(n: Omit<Notice, 'id' | 'time' | 'read'>, toast = true): Notice {
    const item: Notice = { ...n, id: ++seq, time: Date.now(), read: false }
    list.value.unshift(item)
    if (list.value.length > 100) list.value.length = 100
    if (toast) showToast(item)
    return list.value[0]
  }

  function showToast(n: Notice) {
    toasts.value.push(n)
    setTimeout(() => dismissToast(n.id), n.type === 'error' ? 8000 : 4500)
  }

  function dismissToast(id: number) {
    toasts.value = toasts.value.filter((t) => t.id !== id)
  }

  function update(id: number, patch: Partial<Notice>) {
    const n = list.value.find((x) => x.id === id)
    if (n) Object.assign(n, patch)
  }

  function remove(id: number) {
    list.value = list.value.filter((n) => n.id !== id)
  }

  function clear() {
    list.value = list.value.filter((n) => n.progress !== undefined && !n.done)
  }

  function markAllRead() {
    list.value.forEach((n) => (n.read = true))
  }

  function onTask(ev: TaskEvent) {
    const t = ev.task
    tasks.value[t.id] = t
    let n = list.value.find((x) => x.taskId === t.id)
    if (!n) {
      n = push({ type: 'info', title: t.title, progress: t.progress, taskId: t.id }, false)
    }
    n.progress = t.progress
    if (t.status !== 'running' && !n.done) {
      n.done = true
      n.read = false
      n.type = t.status === 'success' ? 'success' : t.status === 'canceled' ? 'warning' : 'error'
      n.content = t.status === 'success' ? '已完成' : t.error || '已取消'
      showToast(n)
      waiters.get(t.id)?.forEach((fn) => fn(t))
      waiters.delete(t.id)
    }
  }

  /** 等待任务结束（成功、失败或取消）。 */
  function waitTask(task: Task): Promise<Task> {
    tasks.value[task.id] = task
    if (!list.value.find((x) => x.taskId === task.id)) {
      push({ type: 'info', title: task.title, progress: 0, taskId: task.id, cancel: () => cancelTask(task.id) }, false)
    }
    return new Promise((resolve) => {
      const cur = tasks.value[task.id]
      if (cur && cur.status !== 'running') return resolve(cur)
      const arr = waiters.get(task.id) ?? []
      arr.push(resolve)
      waiters.set(task.id, arr)
      // 推送可能在订阅前已结束：短暂延迟后查一次
      setTimeout(async () => {
        try {
          const r = await api.get<{ task: Task }>(`/tasks/${task.id}`)
          if (r.task.status !== 'running') onTask({ task: r.task })
        } catch {
          /* 忽略 */
        }
      }, 1500)
    })
  }

  async function cancelTask(id: string) {
    await api.post(`/tasks/${id}/cancel`)
  }

  // 服务监控的异常与恢复
  function onProbe(ev: ProbeEvent) {
    push({
      type: ev.ok ? 'success' : 'error',
      title: ev.ok ? `${ev.name} 已恢复` : `${ev.name} 异常`,
      content: ev.ok ? ev.target : `${ev.target}：${ev.message}`,
    })
  }

  // 服务端主动推送的通知（例如修改超时未确认已自动回滚）
  function onNotice(ev: { level: NoticeType; title: string; body?: string }) {
    push({ type: ev.level, title: ev.title, content: ev.body })
  }

  function start() {
    if (!unsub) unsub = realtime.subscribe('tasks', onTask)
    if (!unsubNotice) unsubNotice = realtime.subscribe('notify', onNotice)
    if (!unsubProbe && useSession().has('probe:view')) unsubProbe = realtime.subscribe('probe-events', onProbe)
  }

  function stop() {
    unsub?.()
    unsubProbe?.()
    unsubNotice?.()
    unsub = null
    unsubProbe = null
    unsubNotice = null
    list.value = []
    toasts.value = []
  }

  return {
    list, toasts, tasks, unread, running,
    push, update, remove, clear, markAllRead, dismissToast, waitTask, cancelTask, start, stop,
  }
})
