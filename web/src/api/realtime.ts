// /api/v1/ws 客户端：一条连接按频道订阅，断线自动重连并恢复订阅。
import { ref } from 'vue'
import { wsURL } from './http'

type Listener = (data: any) => void

class Realtime {
  private ws: WebSocket | null = null
  private listeners = new Map<string, Set<Listener>>()
  private retry = 0
  private timer: number | undefined
  private stopped = true
  readonly connected = ref(false)

  start() {
    if (!this.stopped) return
    this.stopped = false
    this.connect()
  }

  stop() {
    this.stopped = true
    clearTimeout(this.timer)
    this.ws?.close()
    this.ws = null
    this.connected.value = false
  }

  private connect() {
    const ws = new WebSocket(wsURL('/ws'))
    this.ws = ws
    ws.onopen = () => {
      this.retry = 0
      this.connected.value = true
      for (const ch of this.listeners.keys()) this.send({ op: 'sub', ch })
    }
    ws.onmessage = (ev) => {
      let msg: { ch: string; data: unknown }
      try {
        msg = JSON.parse(ev.data)
      } catch {
        return
      }
      this.listeners.get(msg.ch)?.forEach((fn) => fn(msg.data))
    }
    ws.onclose = () => {
      this.connected.value = false
      if (this.ws === ws) this.ws = null
      if (this.stopped) return
      const delay = Math.min(30000, 1000 * 2 ** this.retry++)
      this.timer = window.setTimeout(() => this.connect(), delay)
    }
  }

  private send(m: object) {
    if (this.ws?.readyState === WebSocket.OPEN) this.ws.send(JSON.stringify(m))
  }

  /** 订阅频道，返回取消订阅函数。tasks 等账号级频道无需服务端订阅也会推送。 */
  subscribe(ch: string, fn: Listener): () => void {
    let set = this.listeners.get(ch)
    if (!set) {
      set = new Set()
      this.listeners.set(ch, set)
      this.send({ op: 'sub', ch })
    }
    set.add(fn)
    return () => {
      const s = this.listeners.get(ch)
      if (!s) return
      s.delete(fn)
      if (s.size === 0) {
        this.listeners.delete(ch)
        this.send({ op: 'unsub', ch })
      }
    }
  }
}

export const realtime = new Realtime()
