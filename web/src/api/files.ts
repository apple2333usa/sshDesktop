import { api, buildQuery, request } from './http'
import type { FileEntry, MountInfo, Task, TrashItem } from './types'

const base = (node: string) => `/nodes/${node}/files`

export const filesApi = {
  list: (node: string, path: string) => api.get<{ path: string; entries: FileEntry[] }>(`${base(node)}/list`, { path }),
  stat: (node: string, path: string) => api.get<FileEntry>(`${base(node)}/stat`, { path }),
  mounts: (node: string) => api.get<{ mounts: MountInfo[]; home: string }>(`${base(node)}/mounts`),
  properties: (node: string, path: string) =>
    api.get<{ entry: FileEntry; size?: number; files?: number; dirs?: number; partial?: boolean; atime?: number; ctime?: number }>(
      `${base(node)}/properties`,
      { path },
    ),
  search: (node: string, path: string, q: string, signal?: AbortSignal) =>
    api.get<{ entries: FileEntry[]; partial: boolean }>(`${base(node)}/search`, { path, q }, signal),
  read: (node: string, path: string) =>
    api.get<{ path: string; content: string; version: string; size: number }>(`${base(node)}/content`, { path }),
  write: (node: string, path: string, content: string, version: string, force = false) =>
    api.put<{ version: string }>(`${base(node)}/content`, { path, content, version, force }),
  mkdir: (node: string, path: string) => api.post(`${base(node)}/mkdir`, { path }),
  create: (node: string, path: string) => api.post(`${base(node)}/create`, { path }),
  rename: (node: string, path: string, name: string) => api.post<{ path: string }>(`${base(node)}/rename`, { path, name }),
  /** srcNode 为源文件所在节点，与 node 不同时由面板中转（跨节点复制 / 移动） */
  paste: (node: string, sources: string[], dest: string, mode: 'copy' | 'move', conflict = 'rename', srcNode = node) =>
    api.post<Task>(`${base(node)}/paste`, { sources, dest, mode, conflict, srcNode }),
  remove: (node: string, paths: string[], permanent = false, confirmPassword?: string) =>
    api.post(`${base(node)}/delete`, { paths, permanent }, { confirmPassword }),
  chmod: (node: string, paths: string[], mode: string, recursive: boolean) =>
    api.post(`${base(node)}/chmod`, { paths, mode, recursive }),
  chown: (node: string, paths: string[], owner: string, group: string, recursive: boolean) =>
    api.post(`${base(node)}/chown`, { paths, owner, group, recursive }),
  owners: (node: string) =>
    api.get<{ users: { name: string; id: number }[]; groups: { name: string; id: number }[] }>(`${base(node)}/owners`),
  compress: (node: string, paths: string[], dest: string) => api.post<Task>(`${base(node)}/compress`, { paths, dest }),
  extract: (node: string, path: string, dest: string, overwrite: boolean) =>
    api.post<Task>(`${base(node)}/extract`, { path, dest, overwrite }),
  trash: (node: string) => api.get<TrashItem[]>(`${base(node)}/trash`),
  trashRestore: (node: string, items: { root: string; id: string }[]) =>
    api.post<{ paths: string[] }>(`${base(node)}/trash/restore`, { items }),
  trashDelete: (node: string, items: { root: string; id: string }[], confirmPassword: string) =>
    api.post(`${base(node)}/trash/delete`, { items }, { confirmPassword }),
  trashEmpty: (node: string, confirmPassword: string) => api.post(`${base(node)}/trash/empty`, {}, { confirmPassword }),

  rawURL: (node: string, path: string, inline = true) =>
    `/api/v1${base(node)}/raw${buildQuery({ path, inline: inline ? 1 : 0 })}`,
  downloadURL: (node: string, paths: string[]) => `/api/v1${base(node)}/download${buildQuery({ path: paths })}`,
}

const CHUNK = 8 << 20

export interface UploadProgress {
  loaded: number
  total: number
}

/** 分片上传单个文件，支持断点续传：中断后再次上传同一文件会从已接收的位置继续。 */
export async function uploadFile(
  node: string,
  dir: string,
  file: File,
  opts: { name?: string; overwrite?: boolean; signal?: AbortSignal; onProgress?: (p: UploadProgress) => void } = {},
): Promise<string> {
  const name = opts.name ?? file.name
  const meta = { dir, name, size: file.size, overwrite: !!opts.overwrite }
  let { offset } = await api.post<{ offset: number }>(`${base(node)}/upload/init`, meta)
  opts.onProgress?.({ loaded: offset, total: file.size })
  while (offset < file.size) {
    if (opts.signal?.aborted) throw new DOMException('aborted', 'AbortError')
    const chunk = file.slice(offset, Math.min(offset + CHUNK, file.size))
    const res = await request<{ offset: number }>(`${base(node)}/upload/chunk`, {
      method: 'PUT',
      query: { ...meta, offset },
      raw: chunk,
      headers: { 'Content-Type': 'application/octet-stream' },
      signal: opts.signal,
    })
    offset = res.offset
    opts.onProgress?.({ loaded: offset, total: file.size })
  }
  const done = await api.post<{ path: string }>(`${base(node)}/upload/complete`, meta)
  return done.path
}

/** 触发浏览器下载。 */
export function triggerDownload(url: string) {
  const a = document.createElement('a')
  a.href = url
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
}
