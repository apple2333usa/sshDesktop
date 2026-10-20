// 应用注册表：两种外观（后台 / 桌面）都从这里生成菜单、开始菜单和窗口。
import type { Component } from 'vue'

export interface AppManifest {
  id: string
  /** i18n key */
  title: string
  icon: string
  /** 打开该应用需要的权限点 */
  permission?: string
  /** 只有管理员可用 */
  adminOnly?: boolean
  component: () => Promise<Component | { default: Component }>
  window: { width: number; height: number; minWidth?: number; minHeight?: number; singleton?: boolean }
  /** 能打开的文件类型（fileKind），用于双击打开和“打开方式” */
  fileAssociations?: string[]
  /** 不出现在开始菜单和后台菜单（由其他应用打开，如编辑器、查看器） */
  hidden?: boolean
  /** 后台外观菜单分组 */
  group?: 'system' | 'manage'
}

export const apps: AppManifest[] = [
  {
    id: 'servers',
    title: 'apps.servers',
    icon: 'servers',
    component: () => import('./servers/ServersApp.vue'),
    window: { width: 1040, height: 660, minWidth: 420, minHeight: 320, singleton: true },
    group: 'system',
  },
  {
    id: 'monitor',
    title: 'apps.monitor',
    icon: 'monitor',
    permission: 'monitor:view',
    component: () => import('./monitor/MonitorApp.vue'),
    window: { width: 1000, height: 680, minWidth: 560, minHeight: 400, singleton: true },
    group: 'system',
  },
  {
    id: 'explorer',
    title: 'apps.explorer',
    icon: 'explorer',
    permission: 'files:view',
    component: () => import('./explorer/ExplorerApp.vue'),
    window: { width: 980, height: 620, minWidth: 480, minHeight: 320 },
    group: 'system',
  },
  {
    id: 'terminal',
    title: 'apps.terminal',
    icon: 'terminal',
    permission: 'terminal:use',
    component: () => import('./terminal/TerminalApp.vue'),
    window: { width: 900, height: 560, minWidth: 420, minHeight: 260 },
    group: 'system',
  },
  {
    id: 'processes',
    title: 'apps.processes',
    icon: 'processes',
    permission: 'process:view',
    component: () => import('./processes/ProcessesApp.vue'),
    window: { width: 960, height: 620, minWidth: 560, minHeight: 360, singleton: true },
    group: 'system',
  },
  {
    id: 'services',
    title: 'apps.services',
    icon: 'services',
    permission: 'process:view',
    component: () => import('./services/ServicesApp.vue'),
    window: { width: 1000, height: 640, minWidth: 600, minHeight: 360, singleton: true },
    group: 'system',
  },
  {
    id: 'traffic',
    title: 'apps.traffic',
    icon: 'traffic',
    permission: 'monitor:view',
    component: () => import('./traffic/TrafficApp.vue'),
    window: { width: 960, height: 660, minWidth: 520, minHeight: 360, singleton: true },
    group: 'system',
  },
  {
    id: 'batch',
    title: 'apps.batch',
    icon: 'batch',
    permission: 'batch:run',
    component: () => import('./batch/BatchApp.vue'),
    window: { width: 1040, height: 660, minWidth: 520, minHeight: 360, singleton: true },
    group: 'system',
  },
  {
    id: 'probes',
    title: 'apps.probes',
    icon: 'probes',
    permission: 'probe:view',
    component: () => import('./probes/ProbesApp.vue'),
    window: { width: 1000, height: 640, minWidth: 520, minHeight: 360, singleton: true },
    group: 'system',
  },
  {
    id: 'editor',
    title: 'apps.editor',
    icon: 'editor',
    permission: 'files:view',
    component: () => import('./editor/EditorApp.vue'),
    window: { width: 900, height: 620, minWidth: 420, minHeight: 280 },
    fileAssociations: ['text', 'code', 'config', 'shell', 'log', 'file'],
    hidden: true,
  },
  {
    id: 'viewer',
    title: 'apps.viewer',
    icon: 'viewer',
    permission: 'files:view',
    component: () => import('./viewer/ViewerApp.vue'),
    window: { width: 860, height: 620, minWidth: 360, minHeight: 280 },
    fileAssociations: ['image', 'video', 'audio', 'pdf'],
    hidden: true,
  },
  {
    id: 'users',
    title: 'apps.users',
    icon: 'users',
    permission: 'users:manage',
    adminOnly: true,
    component: () => import('./users/UsersApp.vue'),
    window: { width: 960, height: 620, minWidth: 600, minHeight: 360, singleton: true },
    group: 'manage',
  },
  {
    id: 'audit',
    title: 'apps.audit',
    icon: 'audit',
    permission: 'audit:view',
    component: () => import('./audit/AuditApp.vue'),
    window: { width: 1040, height: 640, minWidth: 600, minHeight: 360, singleton: true },
    group: 'manage',
  },
  {
    id: 'settings',
    title: 'apps.settings',
    icon: 'settings',
    component: () => import('./settings/SettingsApp.vue'),
    window: { width: 920, height: 640, minWidth: 560, minHeight: 400, singleton: true },
    group: 'manage',
  },
]

export const appMap = Object.fromEntries(apps.map((a) => [a.id, a])) as Record<string, AppManifest>

/** 根据文件类型选择默认打开方式；返回 null 表示下载。 */
export function appForFile(kind: string): AppManifest | null {
  if (kind === 'archive' || kind === 'binary') return null
  return apps.find((a) => a.fileAssociations?.includes(kind)) ?? null
}
