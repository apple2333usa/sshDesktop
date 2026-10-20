/** 要恢复的备份：来自备份记录，或备份目标中的文件 */
export interface RestoreSource {
  recordId?: number
  targetId?: number
  key: string
  type?: 'dir' | 'app' | 'panel'
  source?: string
  encrypted: boolean
  /** 计划里保存了密码，可以不填 */
  savedPassword?: boolean
  snapshot?: boolean
}
