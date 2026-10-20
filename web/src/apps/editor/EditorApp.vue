<script setup lang="ts">
// 文本编辑器（CodeMirror 6）：语法高亮、查找替换、Ctrl+S 保存；保存前按版本检查，防止覆盖他人的修改。
import { computed, onMounted, onUnmounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { NButton, NSelect, NSpin, NSwitch, useDialog, useMessage } from 'naive-ui'
import { EditorView, keymap } from '@codemirror/view'
import { Compartment, EditorState } from '@codemirror/state'
import { basicSetup } from 'codemirror'
import { indentWithTab } from '@codemirror/commands'
import { openSearchPanel } from '@codemirror/search'
import { oneDark } from '@codemirror/theme-one-dark'
import { ApiError, Codes } from '@/api/http'
import { filesApi, triggerDownload } from '@/api/files'
import { useAppHost } from '@/apps/host'
import { usePrefs } from '@/stores/prefs'
import { useSession } from '@/stores/session'
import { errorText, formatBytes } from '@/i18n'
import { detectLanguage, languageNames, loadLanguage } from './languages'

const host = useAppHost()
const { t } = useI18n()
const message = useMessage()
const dialog = useDialog()
const prefs = usePrefs()
const session = useSession()

const node = (host.props.node as string) || 'local'
const path = host.props.path as string
const name = path.slice(path.lastIndexOf('/') + 1)

const el = ref<HTMLElement | null>(null)
const view = shallowRef<EditorView | null>(null)
const loading = ref(true)
const loadError = ref('')
const errorCode = ref(0)
const saving = ref(false)
const dirty = ref(false)
const version = ref('')
const size = ref(0)
const lang = ref<string | null>(null)
const wrap = ref(false)
const cursor = ref({ line: 1, col: 1 })
const crlf = ref(false)
const readOnly = computed(() => !session.has('files:manage'))

const langComp = new Compartment()
const themeComp = new Compartment()
const wrapComp = new Compartment()
let savedDoc = ''

function updateTitle() {
  host.setTitle((dirty.value ? '● ' : '') + name)
}
watch(dirty, updateTitle, { immediate: true })

const langOptions = computed(() => [
  { label: t('editor.plainText'), value: '' },
  ...Object.entries(languageNames).map(([k, v]) => ({ label: v, value: k })),
])

async function setLanguage(key: string | null) {
  lang.value = key || null
  const ext = await loadLanguage(lang.value)
  view.value?.dispatch({ effects: langComp.reconfigure(ext) })
}

watch(
  () => prefs.dark,
  (d) => view.value?.dispatch({ effects: themeComp.reconfigure(d ? oneDark : []) }),
)
watch(wrap, (w) => view.value?.dispatch({ effects: wrapComp.reconfigure(w ? EditorView.lineWrapping : []) }))

async function load() {
  loading.value = true
  loadError.value = ''
  try {
    const r = await filesApi.read(node, path)
    version.value = r.version
    size.value = r.size
    crlf.value = r.content.includes('\r\n')
    const doc = crlf.value ? r.content.replace(/\r\n/g, '\n') : r.content
    savedDoc = doc
    create(doc)
    await setLanguage(detectLanguage(path, doc.slice(0, 200).split('\n')[0]))
  } catch (e) {
    loadError.value = errorText(e)
    errorCode.value = e instanceof ApiError ? e.code : 0
  } finally {
    loading.value = false
  }
}

function create(doc: string) {
  view.value?.destroy()
  const state = EditorState.create({
    doc,
    extensions: [
      basicSetup,
      keymap.of([
        indentWithTab,
        { key: 'Mod-s', preventDefault: true, run: () => (save(), true) },
      ]),
      langComp.of([]),
      themeComp.of(prefs.dark ? oneDark : []),
      wrapComp.of(wrap.value ? EditorView.lineWrapping : []),
      EditorState.readOnly.of(readOnly.value),
      EditorView.updateListener.of((u) => {
        if (u.docChanged) dirty.value = u.state.doc.toString() !== savedDoc
        if (u.selectionSet || u.docChanged) {
          const pos = u.state.selection.main.head
          const line = u.state.doc.lineAt(pos)
          cursor.value = { line: line.number, col: pos - line.from + 1 }
        }
      }),
      EditorView.theme({ '&': { height: '100%' }, '.cm-scroller': { fontFamily: 'var(--sd-mono)', fontSize: '13px' } }),
    ],
  })
  view.value = new EditorView({ state, parent: el.value! })
  view.value.focus()
}

async function save(force = false): Promise<boolean> {
  if (!view.value || readOnly.value || saving.value) return false
  saving.value = true
  let content = view.value.state.doc.toString()
  const snapshot = content
  if (crlf.value) content = content.replace(/\n/g, '\r\n')
  try {
    const r = await filesApi.write(node, path, content, version.value, force)
    version.value = r.version
    savedDoc = snapshot
    dirty.value = view.value.state.doc.toString() !== savedDoc
    size.value = new Blob([content]).size
    message.success(t('editor.saved'))
    return true
  } catch (e) {
    if (e instanceof ApiError && e.code === Codes.FileModified) {
      saving.value = false
      return conflict()
    }
    message.error(errorText(e))
    return false
  } finally {
    saving.value = false
  }
}

function conflict(): Promise<boolean> {
  return new Promise((resolve) => {
    const d = dialog.warning({
      title: t('editor.conflictTitle'),
      content: t('editor.conflictContent'),
      positiveText: t('editor.overwrite'),
      negativeText: t('editor.reloadDiscard'),
      closable: true,
      onPositiveClick: async () => resolve(await save(true)),
      onNegativeClick: async () => {
        await load()
        dirty.value = false
        resolve(false)
      },
      onClose: () => resolve(false),
      onMaskClick: () => {
        d.destroy()
        resolve(false)
      },
    })
  })
}

host.onBeforeClose(
  () =>
    new Promise<boolean>((resolve) => {
      if (!dirty.value) return resolve(true)
      dialog.warning({
        title: t('editor.unsavedTitle'),
        content: t('editor.unsavedContent', { name }),
        positiveText: t('editor.save'),
        negativeText: t('editor.dontSave'),
        closable: true,
        onPositiveClick: async () => resolve(await save()),
        onNegativeClick: () => resolve(true),
        onClose: () => resolve(false),
        onMaskClick: () => resolve(false),
      })
    }),
)

function beforeUnload(e: BeforeUnloadEvent) {
  if (dirty.value) e.preventDefault()
}

onMounted(() => {
  window.addEventListener('beforeunload', beforeUnload)
  load()
})
onUnmounted(() => {
  window.removeEventListener('beforeunload', beforeUnload)
  view.value?.destroy()
})

function find() {
  if (view.value) openSearchPanel(view.value)
}
</script>

<template>
  <div class="editor" data-testid="editor">
    <div class="sd-toolbar">
      <NButton size="small" type="primary" :disabled="readOnly || !dirty" :loading="saving" data-testid="editor-save" @click="save()">
        {{ t('editor.save') }} (Ctrl+S)
      </NButton>
      <NButton size="small" quaternary :disabled="!view" @click="find">{{ t('editor.find') }}</NButton>
      <NButton size="small" quaternary @click="load">{{ t('editor.reload') }}</NButton>
      <NButton size="small" quaternary @click="triggerDownload(filesApi.downloadURL(node, [path]))">{{ t('explorer.download') }}</NButton>
      <span class="opt">{{ t('editor.wrap') }} <NSwitch v-model:value="wrap" size="small" /></span>
      <span class="path sd-muted sd-mono sd-ellipsis" :title="path">{{ path }}</span>
    </div>
    <div class="body">
      <NSpin v-if="loading" class="center" />
      <div v-else-if="loadError" class="center err">
        <div>{{ loadError }}</div>
        <NButton v-if="errorCode === Codes.FileTooLarge || errorCode === Codes.BinaryFile" size="small" @click="triggerDownload(filesApi.downloadURL(node, [path]))">
          {{ t('explorer.download') }}
        </NButton>
      </div>
      <div v-show="!loading && !loadError" ref="el" class="cm" />
    </div>
    <div class="status sd-muted">
      <span>{{ t('editor.position', cursor) }}</span>
      <span>UTF-8</span>
      <span>{{ crlf ? 'CRLF' : 'LF' }}</span>
      <span>{{ formatBytes(size) }}</span>
      <span v-if="readOnly" class="ro">{{ t('editor.readOnly') }}</span>
      <NSelect
        class="lang"
        size="tiny"
        :value="lang ?? ''"
        :options="langOptions"
        :consistent-menu-width="false"
        filterable
        @update:value="(v: string) => setLanguage(v)"
      />
    </div>
  </div>
</template>

<style scoped>
.editor {
  height: 100%;
  display: flex;
  flex-direction: column;
  background: var(--sd-surface);
}
.opt {
  display: inline-flex;
  gap: 6px;
  align-items: center;
  font-size: 13px;
}
.path {
  margin-left: auto;
  font-size: 12px;
  max-width: 40%;
}
.body {
  flex: 1;
  min-height: 0;
  position: relative;
  border-top: 1px solid var(--sd-border);
}
.cm {
  height: 100%;
}
.center {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
  align-items: center;
  justify-content: center;
}
.err {
  color: var(--sd-danger);
  padding: 20px;
  text-align: center;
}
.status {
  height: 26px;
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 0 12px;
  font-size: 12px;
  border-top: 1px solid var(--sd-border);
}
.ro {
  color: var(--sd-warning);
}
.lang {
  margin-left: auto;
  width: 130px;
}
</style>
