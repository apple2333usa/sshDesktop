export interface PermInfo {
  key: string
  module: string
  rootEquivalent: boolean
  adminOnly: boolean
}

export interface Me {
  user: {
    id: number
    username: string
    roleId: number
    roleName: string
    isAdmin: boolean
    lastLoginAt: string | null
    lastLoginIp: string
  }
  permissions: string[]
  catalog: PermInfo[]
  defaultAppearance: 'auto' | 'desktop' | 'admin'
  version: string
}

export interface Metric {
  ts: number
  cpu: number
  load1: number
  load5: number
  load15: number
  memUsed: number
  memTotal: number
  swapUsed: number
  swapTotal: number
  diskUsed: number
  diskTotal: number
  diskReadBps: number
  diskWriteBps: number
  netRxBps: number
  netTxBps: number
  tcpConns: number
  procs: number
}

export interface DiskUsage {
  mount: string
  device: string
  fsType: string
  total: number
  used: number
}

export interface Sample extends Metric {
  uptime: number
  cpuCores: number[] | null
  disks: DiskUsage[] | null
}

export interface HostInfo {
  hostname: string
  os: string
  platform: string
  platformVersion: string
  kernel: string
  arch: string
  cpuModel: string
  cpuCores: number
  cpuThreads: number
  bootTime: number
  virtualization: string
}

export interface SystemInfo {
  host: HostInfo
  uptime: number
  addrs: string[] | null
  version: string
  panel: { port: number; domain: string; https: boolean; listen: string; dataDir: string }
  time: number
}

export interface FileEntry {
  name: string
  path: string
  type: 'dir' | 'file' | 'link' | 'other'
  isDir: boolean
  size: number
  mode: string
  perm: string
  owner: string
  group: string
  uid: number
  gid: number
  mtime: number
  linkTarget?: string
  broken?: boolean
}

export interface MountInfo {
  point: string
  device: string
  fsType: string
  total: number
  used: number
  free: number
}

export interface TrashItem {
  id: string
  root: string
  name: string
  original: string
  deletedAt: number
  isDir: boolean
  size: number
}

export interface Task {
  id: string
  kind: string
  title: string
  userId: number
  node: string
  status: 'running' | 'success' | 'failed' | 'canceled'
  progress: number
  error: string
  createdAt: string
  finishedAt: string | null
}

export interface TaskEvent {
  task: Task
  log?: string
}

export interface TerminalSession {
  id: string
  node: string
  username: string
  title: string
  createdAt: string
  attached: number
  exited: boolean
}

export interface Proc {
  pid: number
  ppid: number
  name: string
  user: string
  state: string
  cpu: number
  rss: number
  mem: number
  threads: number
  started: number
  cmdline: string
}

export interface Unit {
  name: string
  load: string
  active: string
  sub: string
  description: string
  unitFile: string
}

export interface UserRow {
  id: number
  username: string
  roleId: number
  roleName: string
  isAdmin: boolean
  disabled: boolean
  lastLoginAt: string | null
  lastLoginIp: string
  createdAt: string
}

export interface RoleRow {
  id: number
  name: string
  description: string
  isAdmin: boolean
  permissions: string[]
  userCount: number
}

export interface SessionRow {
  id: number
  userId: number
  username: string
  ip: string
  userAgent: string
  createdAt: string
  lastSeenAt: string
  current: boolean
}

export interface BanRow {
  ip: string
  reason: string
  until: string
  createdAt: string
}

export interface AuditRow {
  id: number
  at: string
  userId: number
  username: string
  ip: string
  node: string
  module: string
  action: string
  target: string
  success: boolean
  detail: string
  durationMs: number
}

export interface RuntimeSettings {
  sessionTimeoutHours: number
  banMaxFailures: number
  banWindowMinutes: number
  banMinutes: number
  trustedProxy: boolean
  defaultAppearance: 'auto' | 'desktop' | 'admin'
  terminalKeepMinutes: number
  trashRetentionDays: number
  auditRetentionDays: number
}
