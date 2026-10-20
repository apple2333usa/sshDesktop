import { describe, expect, it } from 'vitest'
import { formatBytes, formatDuration } from './index'
import { fileKind } from '@/components/icons'
import { detectLanguage } from '@/apps/editor/languages'

describe('格式化', () => {
  it('字节数', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(264.6159)).toBe('265 B')
    expect(formatBytes(1536)).toBe('1.5 KB')
    expect(formatBytes(-1)).toBe('-')
  })
  it('时长', () => {
    expect(formatDuration(59)).toBe('0 分钟')
    expect(formatDuration(3720)).toBe('1 小时 2 分钟')
    expect(formatDuration(90000)).toBe('1 天 1 小时')
  })
})

describe('文件类型', () => {
  it('按扩展名识别', () => {
    expect(fileKind('a.tar.gz', false)).toBe('archive')
    expect(fileKind('photo.JPG', false)).toBe('image')
    expect(fileKind('.bashrc', false)).toBe('shell')
    expect(fileKind('docs', true)).toBe('folder')
  })
  it('编辑器语言', () => {
    expect(detectLanguage('/etc/nginx/nginx.conf')).toBe('nginx')
    expect(detectLanguage('/srv/app/Dockerfile')).toBe('dockerfile')
    expect(detectLanguage('/root/run', '#!/bin/bash')).toBe('sh')
    expect(detectLanguage('/etc/systemd/system/x.service')).toBe('ini')
    expect(detectLanguage('/tmp/readme')).toBeNull()
  })
})
