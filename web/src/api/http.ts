// 接口封装：统一响应 { code, message, data }，写请求自动带 X-Requested-With（CSRF 防护）。

export class ApiError extends Error {
  constructor(
    public status: number,
    public code: number,
    message: string,
    public data?: unknown,
  ) {
    super(message)
  }
}

export const Codes = {
  Unauthorized: 1002,
  Forbidden: 1003,
  NotFound: 1004,
  ConfirmRequired: 1007,
  ConfirmFailed: 1008,
  BadCredentials: 2001,
  IPBanned: 2002,
  RetryLater: 2003,
  FileExists: 3002,
  FileModified: 3003,
  TrashUnavailable: 3005,
  FileTooLarge: 3006,
  BinaryFile: 3007,
  SessionGone: 4001,
  HostKeyChanged: 6001,
  NodeOffline: 6002,
  AgentFailed: 6003,
} as const

export interface RequestOptions {
  method?: string
  query?: Record<string, string | number | boolean | undefined | (string | number)[]>
  body?: unknown
  raw?: BodyInit
  headers?: Record<string, string>
  /** 二级危险操作的密码确认 */
  confirmPassword?: string
  signal?: AbortSignal
}

type UnauthorizedHandler = () => void
let onUnauthorized: UnauthorizedHandler | null = null
export function setUnauthorizedHandler(fn: UnauthorizedHandler) {
  onUnauthorized = fn
}

export function buildQuery(query?: RequestOptions['query']): string {
  if (!query) return ''
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined) continue
    if (Array.isArray(v)) v.forEach((x) => p.append(k, String(x)))
    else p.append(k, String(v))
  }
  const s = p.toString()
  return s ? `?${s}` : ''
}

/** 把 UTF-8 字符串编码为 base64（请求头只能放 ASCII）。 */
export function b64(s: string): string {
  const bytes = new TextEncoder().encode(s)
  let bin = ''
  bytes.forEach((b) => (bin += String.fromCharCode(b)))
  return btoa(bin)
}

export async function request<T = unknown>(path: string, opts: RequestOptions = {}): Promise<T> {
  const method = opts.method ?? (opts.body !== undefined || opts.raw !== undefined ? 'POST' : 'GET')
  const headers: Record<string, string> = { 'X-Requested-With': 'sshdesktop', ...opts.headers }
  let body: BodyInit | undefined = opts.raw
  if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }
  if (opts.confirmPassword !== undefined) headers['X-Confirm-Password'] = b64(opts.confirmPassword)
  let resp: Response
  try {
    resp = await fetch(`/api/v1${path}${buildQuery(opts.query)}`, {
      method,
      headers,
      body,
      credentials: 'same-origin',
      signal: opts.signal,
    })
  } catch (e) {
    if ((e as Error).name === 'AbortError') throw e
    throw new ApiError(0, 0, '无法连接到面板，请检查网络或 SSH 隧道')
  }
  let json: { code: number; message: string; data: T } | null = null
  const ct = resp.headers.get('Content-Type') ?? ''
  if (ct.includes('application/json')) json = await resp.json()
  if (!resp.ok || !json || json.code !== 0) {
    const code = json?.code ?? 0
    if (resp.status === 401 && code === Codes.Unauthorized && onUnauthorized) onUnauthorized()
    throw new ApiError(resp.status, code, json?.message || `请求失败（HTTP ${resp.status}）`, json?.data)
  }
  return json.data
}

export const api = {
  get: <T>(path: string, query?: RequestOptions['query'], signal?: AbortSignal) =>
    request<T>(path, { query, signal }),
  post: <T>(path: string, body?: unknown, extra?: RequestOptions) =>
    request<T>(path, { method: 'POST', body: body ?? {}, ...extra }),
  put: <T>(path: string, body?: unknown, extra?: RequestOptions) =>
    request<T>(path, { method: 'PUT', body: body ?? {}, ...extra }),
  del: <T>(path: string, extra?: RequestOptions) => request<T>(path, { method: 'DELETE', ...extra }),
}

export function wsURL(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}/api/v1${path}`
}
