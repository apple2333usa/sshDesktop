// 危险操作确认：一级为确认框；二级还需再次输入当前账号密码。
import { reactive } from 'vue'
import { ApiError, Codes } from '@/api/http'

export interface DangerOptions {
  level: 1 | 2
  /** 操作名称，例如“永久删除文件” */
  action: string
  /** 目标节点（主机名 + IP），默认本机 */
  node?: string
  /** 影响范围：文件列表、规则内容、命令全文等 */
  impact?: string | string[]
  /** 确认按钮文字，写成具体动作，例如“删除 3 个文件” */
  confirmText: string
}

interface State extends DangerOptions {
  show: boolean
  error: string
  resolve: ((pw: string | null) => void) | null
}

export const dangerState = reactive<State>({
  show: false,
  level: 1,
  action: '',
  confirmText: '',
  error: '',
  resolve: null,
})

/** 弹出确认框。确认后返回密码（一级为空字符串），取消返回 null。 */
export function confirmDanger(opts: DangerOptions, error = ''): Promise<string | null> {
  return new Promise((resolve) => {
    Object.assign(dangerState, { node: undefined, impact: undefined, ...opts, show: true, error, resolve })
  })
}

/**
 * 确认后执行操作；二级操作密码错误时重新弹框。
 * 返回 fn 的结果，用户取消时返回 undefined。
 */
export async function withDanger<T>(opts: DangerOptions, fn: (password?: string) => Promise<T>): Promise<T | undefined> {
  let error = ''
  for (;;) {
    const pw = await confirmDanger(opts, error)
    if (pw === null) return undefined
    try {
      return await fn(opts.level === 2 ? pw : undefined)
    } catch (e) {
      if (e instanceof ApiError && e.code === Codes.ConfirmFailed) {
        error = e.message
        continue
      }
      throw e
    }
  }
}
