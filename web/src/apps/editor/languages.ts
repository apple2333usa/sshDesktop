// 按文件名选择 CodeMirror 语言，语言包按需加载。
import { StreamLanguage, type LanguageSupport, type StreamParser } from '@codemirror/language'
import type { Extension } from '@codemirror/state'

type Loader = () => Promise<Extension>

const legacy = (load: () => Promise<StreamParser<unknown>>): Loader => async () => StreamLanguage.define(await load())

const loaders: Record<string, { name: string; load: Loader }> = {
  js: { name: 'JavaScript', load: async () => (await import('@codemirror/lang-javascript')).javascript() },
  ts: { name: 'TypeScript', load: async () => (await import('@codemirror/lang-javascript')).javascript({ typescript: true }) },
  jsx: { name: 'JSX', load: async () => (await import('@codemirror/lang-javascript')).javascript({ jsx: true }) },
  tsx: { name: 'TSX', load: async () => (await import('@codemirror/lang-javascript')).javascript({ jsx: true, typescript: true }) },
  json: { name: 'JSON', load: async () => (await import('@codemirror/lang-json')).json() },
  html: { name: 'HTML', load: async () => (await import('@codemirror/lang-html')).html() },
  vue: { name: 'Vue', load: async () => (await import('@codemirror/lang-html')).html() },
  css: { name: 'CSS', load: async () => (await import('@codemirror/lang-css')).css() },
  scss: { name: 'SCSS', load: async () => (await import('@codemirror/lang-css')).css() },
  md: { name: 'Markdown', load: async () => (await import('@codemirror/lang-markdown')).markdown() },
  py: { name: 'Python', load: async () => (await import('@codemirror/lang-python')).python() },
  go: { name: 'Go', load: async () => (await import('@codemirror/lang-go')).go() },
  sql: { name: 'SQL', load: async () => (await import('@codemirror/lang-sql')).sql() },
  xml: { name: 'XML', load: async () => (await import('@codemirror/lang-xml')).xml() },
  yaml: { name: 'YAML', load: async () => (await import('@codemirror/lang-yaml')).yaml() },
  sh: { name: 'Shell', load: legacy(async () => (await import('@codemirror/legacy-modes/mode/shell')).shell) },
  nginx: { name: 'Nginx', load: legacy(async () => (await import('@codemirror/legacy-modes/mode/nginx')).nginx) },
  ini: { name: 'INI', load: legacy(async () => (await import('@codemirror/legacy-modes/mode/properties')).properties) },
  toml: { name: 'TOML', load: legacy(async () => (await import('@codemirror/legacy-modes/mode/toml')).toml) },
  dockerfile: { name: 'Dockerfile', load: legacy(async () => (await import('@codemirror/legacy-modes/mode/dockerfile')).dockerFile) },
  lua: { name: 'Lua', load: legacy(async () => (await import('@codemirror/legacy-modes/mode/lua')).lua) },
  rust: { name: 'Rust', load: legacy(async () => (await import('@codemirror/legacy-modes/mode/rust')).rust) },
  diff: { name: 'Diff', load: legacy(async () => (await import('@codemirror/legacy-modes/mode/diff')).diff) },
  php: { name: 'PHP', load: async () => (await import('@codemirror/lang-html')).html() },
}

const aliases: Record<string, string> = {
  mjs: 'js', cjs: 'js', yml: 'yaml', markdown: 'md', htm: 'html', bash: 'sh', zsh: 'sh', ksh: 'sh',
  conf: 'ini', cfg: 'ini', properties: 'ini', env: 'ini', service: 'ini', timer: 'ini', socket: 'ini', desktop: 'ini',
  rs: 'rust', svg: 'xml', patch: 'diff', less: 'css',
}

/** 返回语言 key；无法识别时返回 null（纯文本）。 */
export function detectLanguage(path: string, firstLine = ''): string | null {
  const name = path.slice(path.lastIndexOf('/') + 1).toLowerCase()
  if (name === 'dockerfile' || name.startsWith('dockerfile.')) return 'dockerfile'
  if (path.includes('/nginx/') && (name.endsWith('.conf') || !name.includes('.'))) return 'nginx'
  if (['.bashrc', '.profile', '.bash_profile', '.zshrc', '.bash_aliases'].includes(name)) return 'sh'
  if (name === '.env' || name.startsWith('.env.')) return 'ini'
  const ext = name.includes('.') ? name.slice(name.lastIndexOf('.') + 1) : ''
  const key = aliases[ext] ?? ext
  if (loaders[key]) return key
  if (/^#!.*\b(ba|z|k)?sh\b/.test(firstLine)) return 'sh'
  if (/^#!.*python/.test(firstLine)) return 'py'
  return null
}

export const languageNames = Object.fromEntries(Object.entries(loaders).map(([k, v]) => [k, v.name]))

export async function loadLanguage(key: string | null): Promise<Extension> {
  if (!key || !loaders[key]) return []
  return (await loaders[key].load()) as LanguageSupport | Extension
}
