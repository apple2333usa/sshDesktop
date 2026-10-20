// 文件剪贴板：在所有资源管理器窗口和桌面之间共享。
import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useFileClipboard = defineStore('fileClipboard', () => {
  const mode = ref<'copy' | 'move' | null>(null)
  const node = ref('local')
  const paths = ref<string[]>([])

  function set(m: 'copy' | 'move', n: string, p: string[]) {
    mode.value = m
    node.value = n
    paths.value = [...p]
  }

  function clear() {
    mode.value = null
    paths.value = []
  }

  return { mode, node, paths, set, clear }
})
