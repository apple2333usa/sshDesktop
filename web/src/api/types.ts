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

/* ---------- 多服务器 ---------- */

export type NodeState = 'connecting' | 'online' | 'offline' | 'hostkey-mismatch' | 'auth-failed'

export interface NodeStatus {
  state: NodeState
  error?: string
  since: number
  latencyMs: number
}

export interface NodeView {
  id: string
  kind: 'local' | 'ssh'
  name: string
  host: string
  port: number
  username: string
  authType: 'key' | 'password' | ''
  color: string
  sortOrder: number
  trafficResetDay: number
  trafficQuota: number
  trafficQuotaMode: 'sum' | 'rx' | 'tx' | ''
  createdAt: string
  updatedAt: string
  status: NodeStatus
  hostKey?: { type: string; fingerprint: string; confirmedAt: string }
}

export interface HostKeyScan {
  keyType: string
  key: string
  fingerprint: string
}

export interface NodeForm {
  name: string
  host: string
  port: number
  username: string
  authType: 'key' | 'password'
  password: string
  privateKey: string
  passphrase: string
  hostKey: string
  color: string
}

export type MonitorSource = 'local' | 'agent' | 'panel' | 'ssh' | ''

export interface Overview {
  id: string
  source: MonitorSource
  agentVersion?: string
  info: HostInfo | null
  sample: Sample | null
  error?: string
}

export interface TrafficUsage {
  nodeId: string
  start: number
  end: number
  rx: number
  tx: number
  quota: number
  quotaMode: 'sum' | 'rx' | 'tx'
  used: number
  percent: number
  resetDay: number
  supported: boolean
}

export interface TrafficDetail {
  usage: TrafficUsage
  days: { day: number; rx: number; tx: number }[]
  ifaces: { iface: string; rx: number; tx: number }[]
  cycles: TrafficUsage[]
}

export type BatchStatus = 'pending' | 'running' | 'done' | 'failed' | 'timeout' | 'canceled'

export interface BatchResult {
  runId: string
  nodeId: string
  status: BatchStatus
  exitCode: number
  output: string
  error: string
  durationMs: number
  /** 执行中查询时 output 包含的推送片段数 */
  seq?: number
}

export interface BatchRun {
  id: string
  userId: number
  username: string
  command: string
  nodes: string
  timeout: number
  createdAt: string
  finishedAt: string | null
  results: BatchResult[] | null
  running: boolean
}

export interface BatchEvent {
  runId: string
  nodeId: string
  status?: BatchStatus
  output?: string
  seq?: number
  exitCode?: number
  error?: string
  durationMs?: number
  finished?: boolean
}

export interface Snippet {
  id: number
  name: string
  command: string
  createdAt: string
}

export type ProbeType = 'http' | 'tcp' | 'ping' | 'ssl'

export interface Probe {
  id: number
  name: string
  type: ProbeType
  target: string
  interval: number
  timeout: number
  keyword: string
  expectStatus: string
  sslWarnDays: number
  enabled: boolean
  createdAt: string
  updatedAt: string
}

export interface ProbeResult {
  ts: number
  ok: boolean
  latencyMs: number
  message: string
}

export interface ProbeView extends Probe {
  last: ProbeResult | null
  stats: { uptime24h: number; uptime7d: number; uptime30d: number; avgLatency24h: number }
  recent: number[]
}

export interface ProbeEvent {
  probeId: number
  name: string
  type: ProbeType
  target: string
  ok: boolean
  message: string
  ts: number
}

/* ---------- Docker ---------- */

export interface DockerStatus {
  installed: boolean
  running: boolean
  error?: string
  version?: string
  apiVersion?: string
  compose?: string
  socket: string
  info?: {
    Containers: number
    ContainersRunning: number
    ContainersStopped: number
    Images: number
    ServerVersion: string
    Driver: string
    DockerRootDir: string
    OperatingSystem: string
    NCPU: number
    MemTotal: number
  }
}

export interface DockerPort {
  ip?: string
  private: number
  public?: number
  proto: string
}

export interface DockerContainer {
  id: string
  name: string
  image: string
  imageId: string
  command: string
  created: number
  state: string
  status: string
  ports: DockerPort[]
  networks: string[]
  ips: string[]
  mounts: { type: string; name?: string; source: string; destination: string; rw: boolean }[]
  labels: Record<string, string>
  project?: string
  service?: string
  app?: string
}

export interface DockerStats {
  cpuPercent: number
  memUsage: number
  memLimit: number
  netRx: number
  netTx: number
  blockRead: number
  blockWrite: number
  pids: number
}

export interface DockerImage {
  id: string
  tags: string[]
  digests: string[]
  created: number
  size: number
  containers: string[]
}

export interface DockerNetwork {
  id: string
  name: string
  driver: string
  scope: string
  internal: boolean
  created: string
  subnet: string
  gateway: string
  labels: Record<string, string>
  containers: string[]
  builtin: boolean
}

export interface DockerVolume {
  name: string
  driver: string
  mountpoint: string
  created: string
  labels: Record<string, string>
  containers: string[]
  project?: string
}

export interface ComposeProject {
  name: string
  dir: string
  files: string[]
  managed: boolean
  recorded: boolean
  app?: string
  running: number
  total: number
  services: string[]
  status: 'running' | 'partial' | 'stopped' | 'missing'
  created: number
  containers: DockerContainer[]
}

/* ---------- 应用商店 ---------- */

export interface StoreApp {
  source: string
  key: string
  name: string
  description: string
  type: string
  tags: string[]
  website?: string
  github?: string
  document?: string
  architectures: string[]
  memoryRequired?: number
  recommend: number
  limit: number
  crossVersionUpdate: boolean
  versions: string[]
  unsupported?: string
  icon: string
  installed: number
}

export interface StoreTag {
  key: string
  name: string
  sort: number
}

export interface StoreField {
  envKey: string
  type: 'text' | 'password' | 'number' | 'select' | 'apps' | 'service' | string
  default: unknown
  required: boolean
  rule?: string
  random?: boolean
  edit?: boolean
  disabled?: boolean
  multiple?: boolean
  values?: { label: string; value: unknown }[]
  label: string
  hint?: string
  value: string
}

export interface StoreSource {
  id: string
  name: string
  type: 'index' | 'git' | 'dir'
  url: string
  enabled: boolean
  builtin: boolean
  apps?: number
  error?: string
  synced?: number
}

export interface InstalledApp {
  name: string
  source: string
  key: string
  title: string
  version: string
  dir: string
  status: string
  message: string
  createdAt: string
  updatedAt: string
  state: 'running' | 'partial' | 'stopped' | 'installing' | 'failed' | 'upgrading' | string
  running: number
  total: number
  ports: string[]
  icon: string
  upgrades: string[]
  /** 为这个应用创建的反向代理网站 */
  sites: string[]
  external: boolean
  containers: string[]
  busy: boolean
}

/* ---------- 计划任务 ---------- */

export type CronType = 'shell' | 'backup' | 'url' | 'cleanup'

export interface CronJob {
  id: number
  name: string
  type: CronType
  schedule: string
  enabled: boolean
  script: string
  url: string
  planId: number
  cleanDirs: string
  cleanPattern: string
  cleanDays: number
  timeout: number
  keepRuns: number
  lastRunAt: string | null
  lastStatus: string
  next?: number
}

export interface CronRun {
  id: number
  jobId: number
  trigger: string
  status: 'running' | 'success' | 'failed' | 'skipped'
  exitCode: number
  output?: string
  startedAt: string
  finishedAt: string | null
  durationMs: number
}

export interface CronStatus {
  installed: boolean
  running: boolean
  daemon: string
  cronDir: boolean
  file: string
  bin: string
  timezone: string
}

export interface CrontabLine {
  no: number
  kind: 'job' | 'env' | 'comment' | 'blank' | 'other'
  raw: string
  enabled: boolean
  schedule?: string
  user?: string
  command?: string
  next?: number
}

export interface CrontabFile {
  path: string
  user?: string
  system: boolean
  hash: string
  lines: CrontabLine[]
  readOnly: boolean
  error?: string
}

export interface SystemdTimer {
  unit: string
  activates: string
  next: number
  last: number
}

/* ---------- 备份 ---------- */

export type BackupTargetType = 'local' | 'sftp' | 's3' | 'webdav'

export interface BackupTargetConfig {
  path?: string
  endpoint?: string
  region?: string
  bucket?: string
  accessKey?: string
  pathStyle?: boolean
  url?: string
  username?: string
  nodeId?: string
}

export interface BackupTarget {
  id: number
  name: string
  type: BackupTargetType
  config: BackupTargetConfig
  hasSecret: boolean
  plans: number
  createdAt: string
}

export interface BackupRecord {
  id: number
  planId: number
  planName: string
  type: 'dir' | 'app' | 'panel' | 'website'
  source: string
  targetId: number
  targetName: string
  key: string
  size: number
  encrypted: boolean
  status: 'running' | 'success' | 'failed'
  trigger: 'manual' | 'cron' | 'snapshot'
  error: string
  startedAt: string
  finishedAt: string | null
}

export interface BackupPlan {
  id: number
  name: string
  type: 'dir' | 'app' | 'panel' | 'website'
  source: string
  excludes: string
  encrypt: boolean
  keepCount: number
  keepDays: number
  stopApp: boolean
  includeSecretKey: boolean
  targetIds: number[]
  hasPassword: boolean
  schedule: string
  last?: BackupRecord
}

export interface BackupObject {
  key: string
  size: number
  modified: string
  recordId?: number
  encrypted: boolean
}

/* ---------- 网站与证书 ---------- */

export interface NginxRuntime {
  mode: 'host' | 'container'
  binary: string
  flavor: string
  version: string
  conf: string
  siteDir: string
  logDir: string
  container?: string
  composeDir?: string
}

export interface WebRuntimeStatus {
  mode: '' | 'host' | 'container'
  hostBinary: string
  runtime?: NginxRuntime
  running: boolean
  error?: string
  docker: boolean
  ports: Record<string, string>
  sites: number
  included: boolean
}

export interface Website {
  id: number
  name: string
  type: 'proxy' | 'static'
  domains: string
  proxyPass: string
  proxyHost: string
  webSocket: boolean
  root: string
  index: string
  httpPort: number
  httpsPort: number
  https: boolean
  certId: number
  forceHttps: boolean
  http2: boolean
  hsts: boolean
  accessLog: boolean
  custom: string
  raw: string
  enabled: boolean
  appName: string
  takeoverFrom: string
  remark: string
  createdAt: string
  updatedAt: string
  file?: string
  certName?: string
  certEnd?: string | null
}

export interface ExistingServer {
  file: string
  line: number
  names: string[]
  listen: string[]
  ssl: boolean
  root?: string
  proxyPass?: string
  redirect?: string
  cert?: string
  simple: boolean
  reasons: string[]
  certbot: boolean
  defaultSite: boolean
}

export interface ExistingFile {
  path: string
  servers: ExistingServer[]
  takeover?: Website
  reason?: string
  error?: string
}

export interface Certificate {
  id: number
  domains: string
  source: 'acme' | 'upload' | 'file' | 'certbot'
  ca: string
  caUrl: string
  challenge: 'http' | 'dns' | ''
  dnsAccountId: number
  keyType: string
  autoRenew: boolean
  certPath: string
  keyPath: string
  issuer: string
  notBefore: string | null
  notAfter: string | null
  status: 'pending' | 'applying' | 'valid' | 'failed' | 'expired' | string
  error: string
  remark: string
  createdAt: string
  sites: string[]
  panel: boolean
  busy: boolean
  daysLeft: number
}

export interface DNSProvider {
  key: string
  name: string
  fields: string[]
}

export interface CertSettings {
  email: string
  cas: Record<string, string>
  dnsProviders: DNSProvider[]
}

export interface DNSAccount {
  id: number
  name: string
  provider: string
  createdAt: string
  keys: string[]
}

export interface ExternalCert {
  tool: 'certbot' | 'acme.sh'
  name: string
  cert: string
  key: string
  renewal?: string
  info?: { domains: string[]; issuer: string; notBefore: string; notAfter: string }
  error?: string
  migrated: boolean
  notes: string[]
}

/* ---------- 等待确认的修改 ---------- */

export interface PendingChange {
  id: string
  kind: 'panel' | 'firewall' | 'sshd' | string
  title: string
  detail: string[]
  userId: number
  username: string
  started: string
  deadline: string
  extra?: Record<string, unknown>
}

/* ---------- 防火墙 ---------- */

export interface FirewallRule {
  id: string
  action: 'allow' | 'deny' | 'reject' | 'limit' | string
  port: string
  proto: string
  source: string
  family: string
  service?: string
  comment?: string
  raw: string
  editable: boolean
  direction?: string
}

export interface FirewallStatus {
  backend: 'ufw' | 'firewalld' | 'none'
  active: boolean
  version?: string
  zone?: string
  default?: string
  rules: FirewallRule[]
  raw?: string
  rawTool?: string
  installed?: string[]
  managers?: string[]
  notes?: string[]
}

export interface FirewallInfo {
  status: FirewallStatus
  protected: Record<string, string>
  pending?: PendingChange
}

/* ---------- SSH ---------- */

export interface SshdEffective {
  ports: number[]
  listenAddresses: string[]
  passwordAuth: boolean
  permitRootLogin: string
  pubkeyAuth: boolean
  kbdInteractive: boolean
}

export interface SshdStatus {
  installed: boolean
  version: string
  service: string
  active: boolean
  socket: boolean
  effective?: SshdEffective
  error?: string
  files: string[]
  managed: string
  included: boolean
  notes: string[]
}

export interface SshKey {
  line: number
  type: string
  comment: string
  fingerprint: string
  options?: string
  error?: string
}

export interface SysUser {
  name: string
  uid: number
  gid: number
  home: string
}

export interface SshLogin {
  time: string
  success: boolean
  user: string
  ip: string
  port: string
  method: string
  invalid: boolean
}

/* ---------- 环境体检 ---------- */

export interface InspectItem {
  key: string
  title: string
  state: 'managed' | 'partial' | 'unmanaged' | 'info' | 'warning'
  summary: string
  notes: string[]
  actions: string[]
  app?: string
}

export interface InspectReport {
  at: string
  items: InspectItem[]
}

/* ---------- 面板访问方式 ---------- */

export interface PanelAccess {
  https: boolean
  domain: string
  certId: number
  port: number
  listen: string
  public: boolean
  certPath: string
  pending?: PendingChange
}
