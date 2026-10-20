<script setup lang="ts">
// 单个终端面板：xterm.js + 每个会话一条 WebSocket。断线自动重连，服务端会话结束后提示。
import { onMounted, onUnmounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { Terminal, type ITheme } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebLinksAddon } from '@xterm/addon-web-links'
import '@xterm/xterm/css/xterm.css'
import { api, wsURL } from '@/api/http'
import type { TerminalSession } from '@/api/types'

const props = defineProps<{
  node: string
  sid: string
  fontSize: number
  scheme: 'dark' | 'light'
  /** 同步输入：键盘输入交给父组件分发 */
  sync: boolean
  active: boolean
}>()

const emit = defineEmits<{
  title: [string]
  exit: [number]
  gone: []
  input: [string]
  focus: []
}>()

const { t } = useI18n()
const el = ref<HTMLElement | null>(null)
const state = ref<'connecting' | 'open' | 'closed' | 'exited'>('connecting')

const themes: Record<'dark' | 'light', ITheme> = {
  dark: {
    background: '#1e1e1e',
    foreground: '#cccccc',
    cursor: '#ffffff',
    selectionBackground: '#264f78',
    black: '#0c0c0c',
    red: '#c50f1f',
    green: '#13a10e',
    yellow: '#c19c00',
    blue: '#0037da',
    magenta: '#881798',
    cyan: '#3a96dd',
    white: '#cccccc',
    brightBlack: '#767676',
    brightRed: '#e74856',
    brightGreen: '#16c60c',
    brightYellow: '#f9f1a5',
    brightBlue: '#3b78ff',
    brightMagenta: '#b4009e',
    brightCyan: '#61d6d6',
    brightWhite: '#f2f2f2',
  },
  light: {
    background: '#ffffff',
    foreground: '#1b1b1b',
    cursor: '#1b1b1b',
    selectionBackground: '#add6ff',
    black: '#000000',
    red: '#cd3131',
    green: '#00bc00',
    yellow: '#949800',
    blue: '#0451a5',
    magenta: '#bc05bc',
    cyan: '#0598bc',
    white: '#555555',
    brightBlack: '#666666',
    brightRed: '#cd3131',
    brightGreen: '#14ce14',
    brightYellow: '#b5ba00',
    brightBlue: '#0451a5',
    brightMagenta: '#bc05bc',
    brightCyan: '#0598bc',
    brightWhite: '#a5a5a5',
  },
}

const term = new Terminal({
  fontFamily: "'Cascadia Mono', Consolas, 'JetBrains Mono', 'Noto Sans Mono CJK SC', monospace",
  fontSize: props.fontSize,
  cursorBlink: true,
  scrollback: 10000,
  theme: themes[props.scheme],
  allowProposedApi: true,
})
const fit = new FitAddon()
term.loadAddon(fit)
term.loadAddon(new WebLinksAddon())

let ws: WebSocket | null = null
let retry = 0
let retryTimer: number | undefined
let disposed = false
const enc = new TextEncoder()

function sendControl(m: Record<string, unknown>) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(JSON.stringify(m))
}

/** 向会话写入数据（同步输入时由父组件调用）。 */
function send(data: string) {
  if (ws?.readyState === WebSocket.OPEN) ws.send(enc.encode(data))
}

function connect() {
  if (disposed) return
  state.value = 'connecting'
  const sock = new WebSocket(wsURL(`/nodes/${props.node}/terminals/${props.sid}/ws`))
  sock.binaryType = 'arraybuffer'
  ws = sock
  let first = true
  let opened = false
  sock.onopen = () => {
    opened = true
    state.value = 'open'
    retry = 0
    sendControl({ type: 'resize', cols: term.cols, rows: term.rows })
  }
  sock.onmessage = (ev) => {
    if (typeof ev.data === 'string') {
      try {
        const m = JSON.parse(ev.data)
        if (m.type === 'exit') {
          state.value = 'exited'
          term.write(`\r\n\x1b[90m${t('terminal.exited', { code: m.code })}\x1b[0m\r\n`)
          emit('exit', m.code)
        }
      } catch {
        /* 忽略 */
      }
      return
    }
    // 重连时服务端会回放缓冲，先清屏避免重复
    if (first) {
      first = false
      term.reset()
    }
    term.write(new Uint8Array(ev.data))
  }
  sock.onclose = () => {
    if (ws !== sock || disposed || state.value === 'exited') return
    state.value = 'closed'
    // 握手失败时浏览器拿不到状态码，查询会话列表判断会话是否已不存在
    if (!opened) {
      api
        .get<TerminalSession[]>(`/nodes/${props.node}/terminals`)
        .then((list) => {
          if (!list.some((x) => x.id === props.sid)) emit('gone')
          else schedule()
        })
        .catch(schedule)
      return
    }
    schedule()
  }
}

function schedule() {
  if (disposed) return
  const delay = Math.min(1000 * 2 ** retry, 15000)
  retry++
  retryTimer = window.setTimeout(connect, delay)
}

term.onData((d) => {
  if (props.sync) emit('input', d)
  else send(d)
})
term.onBinary((d) => {
  if (ws?.readyState !== WebSocket.OPEN) return
  const buf = new Uint8Array(d.length)
  for (let i = 0; i < d.length; i++) buf[i] = d.charCodeAt(i) & 0xff
  ws.send(buf)
})
term.onTitleChange((title) => {
  emit('title', title)
  sendControl({ type: 'title', title })
})
term.onResize(({ cols, rows }) => sendControl({ type: 'resize', cols, rows }))

let ro: ResizeObserver | null = null
let fitTimer: number | undefined
function refit() {
  clearTimeout(fitTimer)
  fitTimer = window.setTimeout(() => {
    if (el.value && el.value.offsetWidth > 0 && el.value.offsetHeight > 0) fit.fit()
  }, 30)
}

onMounted(() => {
  term.open(el.value!)
  term.textarea?.addEventListener('focus', () => emit('focus'))
  // 复制粘贴：Ctrl+Shift+C / V；有选中内容时 Ctrl+C 复制而不是中断
  term.attachCustomKeyEventHandler((e) => {
    if (e.type !== 'keydown') return true
    const k = e.key.toLowerCase()
    if ((e.ctrlKey && e.shiftKey && k === 'c') || (e.ctrlKey && !e.shiftKey && k === 'c' && term.hasSelection())) {
      navigator.clipboard?.writeText(term.getSelection())
      e.preventDefault()
      return false
    }
    if (e.ctrlKey && e.shiftKey && k === 'v') {
      navigator.clipboard?.readText().then((s) => term.paste(s))
      e.preventDefault()
      return false
    }
    // 留给桌面外观的快捷键
    if (e.altKey && !e.ctrlKey && ['e', 't', 'q', 'w', 'd', 's'].includes(k)) return false
    return true
  })
  ro = new ResizeObserver(refit)
  ro.observe(el.value!)
  fit.fit()
  connect()
  if (props.active) term.focus()
})

onUnmounted(() => {
  disposed = true
  clearTimeout(retryTimer)
  clearTimeout(fitTimer)
  ro?.disconnect()
  ws?.close()
  term.dispose()
})

watch(
  () => props.fontSize,
  (v) => {
    term.options.fontSize = v
    refit()
  },
)
watch(
  () => props.scheme,
  (v) => (term.options.theme = themes[v]),
)
watch(
  () => props.active,
  (v) => {
    if (v) {
      refit()
      term.focus()
    }
  },
)

function reconnect() {
  clearTimeout(retryTimer)
  retry = 0
  ws?.close()
  connect()
}

defineExpose({
  send,
  focus: () => term.focus(),
  fit: refit,
  clear: () => term.clear(),
  paste: (s: string) => term.paste(s),
  getSelection: () => term.getSelection(),
  reconnect,
})
</script>

<template>
  <div class="pane" :class="[scheme, { active }]" @mousedown="emit('focus')">
    <div ref="el" class="xterm-host" />
    <div v-if="state === 'connecting' || state === 'closed'" class="overlay">
      {{ state === 'closed' ? t('terminal.reconnecting') : t('terminal.connecting') }}
    </div>
  </div>
</template>

<style scoped>
.pane {
  position: relative;
  min-width: 0;
  min-height: 0;
  padding: 4px 0 0 6px;
  border: 1px solid transparent;
}
.pane.dark {
  background: #1e1e1e;
}
.pane.light {
  background: #ffffff;
}
.xterm-host {
  width: 100%;
  height: 100%;
}
.overlay {
  position: absolute;
  top: 8px;
  right: 12px;
  padding: 2px 10px;
  border-radius: 10px;
  font-size: 12px;
  background: rgba(128, 128, 128, 0.35);
  color: #fff;
  pointer-events: none;
}
</style>
