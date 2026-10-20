// 文件相关对话框的状态：输入名称、压缩、解压、属性。对话框组件挂在 App 根部，两种外观共用。
import { reactive } from 'vue'
import type { FileEntry } from '@/api/types'

interface PromptState {
  show: boolean
  title: string
  label: string
  value: string
  /** 选中文件名中扩展名之前的部分（重命名时） */
  selectBase: boolean
  resolve: ((v: string | null) => void) | null
}

export const promptState = reactive<PromptState>({
  show: false,
  title: '',
  label: '',
  value: '',
  selectBase: false,
  resolve: null,
})

export function promptText(opts: { title: string; label?: string; value?: string; selectBase?: boolean }): Promise<string | null> {
  return new Promise((resolve) => {
    Object.assign(promptState, { label: '', value: '', selectBase: false, ...opts, show: true, resolve })
  })
}

export interface CompressResult {
  name: string
}

interface CompressState {
  show: boolean
  node: string
  dir: string
  entries: FileEntry[]
  resolve: ((dest: string | null) => void) | null
}

export const compressState = reactive<CompressState>({ show: false, node: 'local', dir: '/', entries: [], resolve: null })

/** 返回压缩包完整路径，取消返回 null。 */
export function askCompress(node: string, dir: string, entries: FileEntry[]): Promise<string | null> {
  return new Promise((resolve) => Object.assign(compressState, { show: true, node, dir, entries, resolve }))
}

interface ExtractState {
  show: boolean
  node: string
  entry: FileEntry | null
  resolve: ((r: { dest: string; overwrite: boolean } | null) => void) | null
}

export const extractState = reactive<ExtractState>({ show: false, node: 'local', entry: null, resolve: null })

export function askExtract(node: string, entry: FileEntry): Promise<{ dest: string; overwrite: boolean } | null> {
  return new Promise((resolve) => Object.assign(extractState, { show: true, node, entry, resolve }))
}

interface PropsState {
  show: boolean
  node: string
  entries: FileEntry[]
  onChanged: (() => void) | null
}

export const propsState = reactive<PropsState>({ show: false, node: 'local', entries: [], onChanged: null })

export function showProperties(node: string, entries: FileEntry[], onChanged?: () => void) {
  Object.assign(propsState, { show: true, node, entries, onChanged: onChanged ?? null })
}
