// 双击打开文件：文本类用编辑器，图片 / 视频 / PDF 用查看器，压缩包进入解压对话框，其他类型下载。
import { toValue, type MaybeRefOrGetter } from 'vue'
import { useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import type { FileEntry } from '@/api/types'
import { filesApi, triggerDownload } from '@/api/files'
import { fileKind } from '@/components/icons'
import { appForFile } from '@/apps/registry'
import { useNotify } from '@/stores/notify'
import { useSession } from '@/stores/session'
import { errorText } from '@/i18n'
import { askExtract } from './dialogs'

export function useFileOpener(nodeRef: MaybeRefOrGetter<string>, openApp: (id: string, props?: Record<string, any>) => void) {
  const message = useMessage()
  const notify = useNotify()
  const session = useSession()
  const { t } = useI18n()

  async function extract(e: FileEntry) {
    const node = toValue(nodeRef)
    if (!session.has('files:manage')) {
      message.warning(t('explorer.noManagePerm'))
      return
    }
    const r = await askExtract(node, e)
    if (!r) return
    try {
      const task = await filesApi.extract(node, e.path, r.dest, r.overwrite)
      await notify.waitTask(task)
    } catch (err) {
      message.error(errorText(err))
    }
  }

  function open(e: FileEntry, as?: 'editor' | 'viewer') {
    const node = toValue(nodeRef)
    if (e.isDir) {
      openApp('explorer', { path: e.path, node })
      return
    }
    if (as) {
      openApp(as, { path: e.path, node })
      return
    }
    const kind = fileKind(e.name, false)
    if (kind === 'archive') {
      extract(e)
      return
    }
    const app = appForFile(kind)
    // 未知类型：小文件尝试用编辑器打开（编辑器会识别二进制并提示），大文件直接下载
    if (!app || (kind === 'file' && e.size > 2 << 20)) {
      triggerDownload(filesApi.downloadURL(node, [e.path]))
      return
    }
    openApp(app.id, { path: e.path, node })
  }

  return { open, extract }
}
