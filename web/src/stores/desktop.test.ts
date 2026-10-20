import { beforeEach, describe, expect, it } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { useDesktop } from './desktop'

describe('桌面窗口管理器', () => {
  beforeEach(() => {
    window.innerWidth = 1600
    window.innerHeight = 948
    setActivePinia(createPinia())
  })

  it('单例应用只打开一个窗口，编辑器按文件去重', () => {
    const d = useDesktop()
    const a = d.open('monitor')
    const b = d.open('monitor')
    expect(a).toBe(b)
    d.open('editor', { path: '/etc/hosts', node: 'local' })
    d.open('editor', { path: '/etc/hosts', node: 'local' })
    d.open('editor', { path: '/etc/fstab', node: 'local' })
    expect(d.windows.filter((w) => w.appId === 'editor')).toHaveLength(2)
    expect(d.open('no-such-app')).toBeNull()
  })

  it('新打开的窗口在最上层并获得焦点', () => {
    const d = useDesktop()
    const a = d.open('explorer')!
    const b = d.open('terminal')!
    expect(d.activeId).toBe(b)
    d.focus(a)
    const wa = d.windows.find((w) => w.id === a)!
    const wb = d.windows.find((w) => w.id === b)!
    expect(wa.z).toBeGreaterThan(wb.z)
  })

  it('保存后在不同分辨率下按比例恢复，且不超出屏幕', () => {
    const d = useDesktop()
    d.open('explorer', { path: '/root' })
    const saved = JSON.parse(JSON.stringify(d.serialize()))
    window.innerWidth = 800
    window.innerHeight = 648
    d.onResize()
    d.restore(saved)
    const w = d.windows[0]
    expect(w.props.path).toBe('/root')
    expect(w.w).toBeLessThanOrEqual(800)
    expect(w.x + 120).toBeLessThanOrEqual(800)
    expect(w.y).toBeGreaterThanOrEqual(0)
  })

  it('读回保存状态之前打开的窗口不会被恢复清掉，并保持在最上层', () => {
    const d = useDesktop()
    d.open('monitor')
    d.open('terminal')
    const saved = JSON.parse(JSON.stringify(d.serialize()))
    d.reset()
    const opened = d.open('monitor')!
    d.open('explorer')
    const explorer = d.activeId
    d.restore(saved)
    // 单例监控以已打开的为准，终端从保存状态恢复
    expect(d.windows.map((w) => w.appId).sort()).toEqual(['explorer', 'monitor', 'terminal'])
    expect(d.windows.find((w) => w.appId === 'monitor')!.id).toBe(opened)
    expect(d.activeId).toBe(explorer)
  })
})
