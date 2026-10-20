// 文件操作：资源管理器窗口和桌面共用。node 可以是 ref（资源管理器在不同服务器之间切换）。
import { toValue, type MaybeRefOrGetter } from 'vue'
import { useDialog, useMessage } from 'naive-ui'
import { useI18n } from 'vue-i18n'
import { ApiError, Codes } from '@/api/http'
import { filesApi, triggerDownload, uploadFile } from '@/api/files'
import type { FileEntry } from '@/api/types'
import { useFileClipboard } from '@/stores/clipboard'
import { useNotify } from '@/stores/notify'
import { useHost } from '@/stores/host'
import { useNodes } from '@/stores/nodes'
import { withDanger } from '@/components/danger'
import { errorText, formatBytes } from '@/i18n'
import { askCompress, promptText, showProperties } from './dialogs'

export const join = (dir: string, name: string) => (dir === '/' ? `/${name}` : `${dir}/${name}`)
export const parentOf = (p: string) => p.slice(0, p.lastIndexOf('/')) || '/'

export function useFileOps(nodeRef: MaybeRefOrGetter<string>, refresh: () => void) {
  const message = useMessage()
  const dialog = useDialog()
  const notify = useNotify()
  const clip = useFileClipboard()
  const host = useHost()
  const nodes = useNodes()
  const { t } = useI18n()
  // 每次操作时取当前节点
  const nodeId = () => toValue(nodeRef)
  const nodeLabel = () => (nodeId() === 'local' ? host.label : nodes.label(nodeId()))

  const fail = (e: unknown) => message.error(errorText(e))

  function ask(title: string, content: string): Promise<boolean> {
    return new Promise((resolve) => {
      dialog.warning({
        title,
        content,
        positiveText: t('common.overwrite'),
        negativeText: t('common.skip'),
        onPositiveClick: () => resolve(true),
        onNegativeClick: () => resolve(false),
        onClose: () => resolve(false),
        onMaskClick: () => resolve(false),
      })
    })
  }

  function copy(entries: FileEntry[]) {
    if (!entries.length) return
    clip.set('copy', nodeId(), entries.map((e) => e.path))
    message.info(t('explorer.copied', { n: entries.length }))
  }

  function cut(entries: FileEntry[]) {
    if (!entries.length) return
    clip.set('move', nodeId(), entries.map((e) => e.path))
    message.info(t('explorer.cut', { n: entries.length }))
  }

  /** 复制或移动到当前节点的 dest；srcNode 为源文件所在节点，不同时由面板中转。 */
  async function transfer(sources: string[], dest: string, mode: 'copy' | 'move', srcNode = nodeId()) {
    if (srcNode !== nodeId()) message.info(t('explorer.crossNode', { mode: mode === 'copy' ? t('explorer.copyAction') : t('explorer.cutAction') }))
    try {
      const task = await filesApi.paste(nodeId(), sources, dest, mode, 'rename', srcNode)
      const done = await notify.waitTask(task)
      if (done.status === 'failed') message.error(done.error)
    } catch (e) {
      fail(e)
    } finally {
      refresh()
    }
  }

  async function paste(dest: string) {
    if (!clip.mode || !clip.paths.length) return
    const mode = clip.mode
    const paths = clip.paths
    const src = clip.node
    if (mode === 'move') clip.clear()
    await transfer(paths, dest, mode, src)
  }

  async function permanentDelete(paths: string[]) {
    try {
      const r = await withDanger(
        {
          level: 2,
          action: t('explorer.permanentDeleteAction'),
          node: nodeLabel(),
          impact: paths,
          confirmText: t('explorer.deleteN', { n: paths.length }),
        },
        (pw) => filesApi.remove(nodeId(), paths, true, pw),
      )
      if (r !== undefined) message.success(t('explorer.deleted'))
    } catch (e) {
      fail(e)
    } finally {
      refresh()
    }
  }

  /** 删除：默认移入回收站（一级确认）；无法移入回收站的改为二级确认后永久删除。 */
  async function remove(entries: FileEntry[], permanent = false) {
    if (!entries.length) return
    const paths = entries.map((e) => e.path)
    if (permanent) return permanentDelete(paths)
    try {
      const r = await withDanger(
        {
          level: 1,
          action: t('explorer.trashAction'),
          node: nodeLabel(),
          impact: paths,
          confirmText: t('explorer.trashN', { n: paths.length }),
        },
        () => filesApi.remove(nodeId(), paths),
      )
      if (r === undefined) return
    } catch (e) {
      if (e instanceof ApiError && e.code === Codes.TrashUnavailable) {
        const failed = ((e.data as { paths?: string[] })?.paths ?? paths) as string[]
        message.warning(e.message)
        refresh()
        return permanentDelete(failed)
      }
      fail(e)
    } finally {
      refresh()
    }
  }

  async function rename(e: FileEntry) {
    const name = await promptText({ title: t('explorer.rename'), value: e.name, selectBase: !e.isDir })
    if (!name || name === e.name) return
    try {
      await filesApi.rename(nodeId(), e.path, name)
    } catch (err) {
      fail(err)
    } finally {
      refresh()
    }
  }

  async function newFolder(dir: string): Promise<string | null> {
    const name = await promptText({ title: t('explorer.newFolder'), value: t('explorer.newFolderName') })
    if (!name) return null
    try {
      await filesApi.mkdir(nodeId(), join(dir, name))
      return name
    } catch (err) {
      fail(err)
      return null
    } finally {
      refresh()
    }
  }

  async function newFile(dir: string): Promise<string | null> {
    const name = await promptText({ title: t('explorer.newFile'), value: t('explorer.newFileName'), selectBase: true })
    if (!name) return null
    try {
      await filesApi.create(nodeId(), join(dir, name))
      return name
    } catch (err) {
      fail(err)
      return null
    } finally {
      refresh()
    }
  }

  function download(entries: FileEntry[]) {
    if (!entries.length) return
    triggerDownload(filesApi.downloadURL(nodeId(), entries.map((e) => e.path)))
  }

  async function compress(dir: string, entries: FileEntry[]) {
    if (!entries.length) return
    const dest = await askCompress(nodeId(), dir, entries)
    if (!dest) return
    try {
      const task = await filesApi.compress(nodeId(), entries.map((e) => e.path), dest)
      await notify.waitTask(task)
    } catch (err) {
      fail(err)
    } finally {
      refresh()
    }
  }

  function properties(entries: FileEntry[]) {
    if (entries.length) showProperties(nodeId(), entries, refresh)
  }

  /** 上传浏览器中的文件（含拖入的文件夹）。 */
  async function upload(items: { file: File; rel: string }[], dir: string) {
    if (!items.length) return
    const node = nodeId()
    const total = items.reduce((s, x) => s + x.file.size, 0)
    const ctl = new AbortController()
    const n = notify.push(
      { type: 'info', title: t('explorer.uploading', { n: items.length }), content: formatBytes(total), progress: 0, cancel: () => ctl.abort() },
      false,
    )
    let loadedBefore = 0
    const made = new Set<string>()
    let overwriteAll: boolean | null = null
    try {
      for (const it of items) {
        // 先创建文件夹结构
        const parts = it.rel.split('/').slice(0, -1)
        let cur = dir
        for (const p of parts) {
          cur = join(cur, p)
          if (!made.has(cur)) {
            made.add(cur)
            await filesApi.mkdir(node, cur).catch(() => undefined)
          }
        }
        const opts = {
          signal: ctl.signal,
          overwrite: overwriteAll ?? false,
          onProgress: (p: { loaded: number }) => {
            notify.update(n.id, { progress: total ? ((loadedBefore + p.loaded) / total) * 100 : 100 })
          },
        }
        try {
          await uploadFile(node, cur, it.file, opts)
        } catch (e) {
          if (e instanceof ApiError && e.code === Codes.FileExists) {
            if (overwriteAll === null) overwriteAll = await ask(t('explorer.fileExists'), t('explorer.overwriteConfirm', { name: it.file.name }))
            if (!overwriteAll) {
              loadedBefore += it.file.size
              continue
            }
            await uploadFile(node, cur, it.file, { ...opts, overwrite: true })
          } else throw e
        }
        loadedBefore += it.file.size
      }
      notify.update(n.id, { progress: 100, done: true, type: 'success', content: t('explorer.uploadDone') })
    } catch (e) {
      const aborted = (e as Error).name === 'AbortError'
      notify.update(n.id, { done: true, type: aborted ? 'warning' : 'error', content: aborted ? t('explorer.uploadCanceled') : errorText(e) })
      if (!aborted) fail(e)
    } finally {
      refresh()
    }
  }

  return { copy, cut, paste, transfer, remove, permanentDelete, rename, newFolder, newFile, download, compress, properties, upload }
}

/** 从拖放数据中收集文件（支持文件夹递归）。 */
export async function collectDropped(dt: DataTransfer): Promise<{ file: File; rel: string }[]> {
  const out: { file: File; rel: string }[] = []
  const entries = Array.from(dt.items)
    .map((i) => (i.kind === 'file' ? i.webkitGetAsEntry?.() : null))
    .filter(Boolean) as FileSystemEntry[]
  if (!entries.length) {
    Array.from(dt.files).forEach((f) => out.push({ file: f, rel: f.name }))
    return out
  }
  async function walk(e: FileSystemEntry, prefix: string) {
    if (e.isFile) {
      const f = await new Promise<File>((res, rej) => (e as FileSystemFileEntry).file(res, rej))
      out.push({ file: f, rel: prefix + f.name })
    } else if (e.isDirectory) {
      const reader = (e as FileSystemDirectoryEntry).createReader()
      for (;;) {
        const batch = await new Promise<FileSystemEntry[]>((res, rej) => reader.readEntries(res, rej))
        if (!batch.length) break
        for (const c of batch) await walk(c, `${prefix}${e.name}/`)
      }
    }
  }
  for (const e of entries) await walk(e, '')
  return out
}
