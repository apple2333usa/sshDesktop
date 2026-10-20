// 应用宿主：应用组件不知道自己在窗口里还是页面里，只通过 useAppHost() 获得这些能力。
import { inject, type InjectionKey, type Ref } from 'vue'

export interface AppHost {
  mode: 'desktop' | 'admin'
  windowId: string | null
  /** 打开应用时传入的参数，以及应用自己保存的内部状态（会随桌面状态保存） */
  props: Record<string, any>
  /** 窗口处于前台（或页面可见），实时刷新可在不活动时暂停 */
  active: Ref<boolean>
  /** 窗口未最小化 */
  visible: Ref<boolean>
  setTitle(title: string): void
  /** 合并保存应用内部状态（如当前目录、终端会话 ID） */
  setState(patch: Record<string, any>): void
  close(): void
  openApp(id: string, props?: Record<string, any>): void
  /** 注册关闭前的检查（如有未保存的修改），返回 false 阻止关闭 */
  onBeforeClose(fn: () => boolean | Promise<boolean>): void
}

export const AppHostKey: InjectionKey<AppHost> = Symbol('AppHost')

export function useAppHost(): AppHost {
  const host = inject(AppHostKey)
  if (!host) throw new Error('useAppHost 必须在应用宿主内使用')
  return host
}
