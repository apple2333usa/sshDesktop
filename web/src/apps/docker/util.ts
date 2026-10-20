// Docker 应用的小工具函数。
import type { DockerPort } from '@/api/types'

export function stateType(state: string): 'success' | 'warning' | 'error' | 'default' | 'info' {
  switch (state) {
    case 'running':
      return 'success'
    case 'paused':
    case 'restarting':
    case 'partial':
      return 'warning'
    case 'dead':
    case 'missing':
      return 'error'
    case 'created':
      return 'info'
  }
  return 'default'
}

export function portText(p: DockerPort): string {
  if (!p.public) return `${p.private}/${p.proto}`
  const ip = p.ip && p.ip !== '0.0.0.0' && p.ip !== '::' ? p.ip + ':' : ''
  return `${ip}${p.public}→${p.private}${p.proto === 'tcp' ? '' : '/' + p.proto}`
}

export function shortId(id: string): string {
  return id.replace(/^sha256:/, '').slice(0, 12)
}
