// 彩色应用图标与文件类型图标（自绘 SVG，风格参考 Windows 11 Fluent 图标）。

const g = (id: string, a: string, b: string) =>
  `<defs><linearGradient id="${id}" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${a}"/><stop offset="1" stop-color="${b}"/></linearGradient></defs>`

const folder = (front = 'url(#fF)') =>
  `${g('fB', '#f5b40a', '#e09c00')}${g('fF', '#ffd75e', '#fbc02d')}` +
  `<path d="M4 12a4 4 0 0 1 4-4h11l4 4h17a4 4 0 0 1 4 4v20a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4z" fill="url(#fB)"/>` +
  `<path d="M4 18a4 4 0 0 1 4-4h32a4 4 0 0 1 4 4v18a4 4 0 0 1-4 4H8a4 4 0 0 1-4-4z" fill="${front}"/>`

const doc = (color: string, inner: string) =>
  `<path d="M11 4h18l10 10v28a3 3 0 0 1-3 3H11a3 3 0 0 1-3-3V7a3 3 0 0 1 3-3z" fill="#fff" stroke="#c8c8c8"/>` +
  `<path d="M29 4v7a3 3 0 0 0 3 3h7z" fill="#e3e3e3"/>` +
  `<rect x="8" y="30" width="22" height="11" rx="2" fill="${color}"/>${inner}`

const label = (t: string) =>
  `<text x="19" y="38.6" font-size="7.5" font-family="Segoe UI, Arial, sans-serif" font-weight="700" fill="#fff" text-anchor="middle">${t}</text>`

export const appIcons: Record<string, string> = {
  explorer: folder() + `<rect x="4" y="18" width="40" height="4" fill="#3a96dd" opacity=".85"/>`,
  thispc:
    `${g('pcS', '#4fb3ff', '#0067c0')}<rect x="5" y="8" width="38" height="26" rx="3" fill="#2c2c2c"/>` +
    `<rect x="7" y="10" width="34" height="22" rx="1.5" fill="url(#pcS)"/>` +
    `<path d="M18 34h12l2 6H16z" fill="#9e9e9e"/><rect x="13" y="39" width="22" height="3" rx="1.5" fill="#bdbdbd"/>`,
  terminal:
    `${g('tmB', '#3d3d3d', '#1e1e1e')}<rect x="4" y="7" width="40" height="34" rx="5" fill="url(#tmB)"/>` +
    `<rect x="4" y="7" width="40" height="7" rx="5" fill="#505050"/><rect x="4" y="11" width="40" height="3" fill="#505050"/>` +
    `<path d="M11 21l7 5-7 5" stroke="#d4d4d4" stroke-width="3" fill="none" stroke-linecap="round" stroke-linejoin="round"/>` +
    `<path d="M21 32h12" stroke="#d4d4d4" stroke-width="3" stroke-linecap="round"/>`,
  monitor:
    `${g('moB', '#2ec8a0', '#0b8a7a')}<rect x="4" y="6" width="40" height="36" rx="6" fill="url(#moB)"/>` +
    `<path d="M9 28h7l4-11 6 17 4-10h9" stroke="#fff" stroke-width="3" fill="none" stroke-linecap="round" stroke-linejoin="round"/>`,
  processes:
    `${g('prB', '#4f8cff', '#2b5dd8')}<rect x="4" y="6" width="40" height="36" rx="6" fill="url(#prB)"/>` +
    `<rect x="11" y="24" width="5" height="11" rx="1.5" fill="#fff"/><rect x="19.5" y="15" width="5" height="20" rx="1.5" fill="#fff"/>` +
    `<rect x="28" y="19" width="5" height="16" rx="1.5" fill="#fff" opacity=".85"/><rect x="36" y="27" width="2" height="8" rx="1" fill="#fff" opacity=".6"/>`,
  services:
    `${g('svB', '#9b6cf7', '#6a3fd1')}<rect x="4" y="6" width="40" height="36" rx="6" fill="url(#svB)"/>` +
    `<circle cx="24" cy="24" r="6" fill="none" stroke="#fff" stroke-width="3"/>` +
    `<path d="M24 11v5M24 32v5M11 24h5M32 24h5M14.8 14.8l3.5 3.5M29.7 29.7l3.5 3.5M14.8 33.2l3.5-3.5M29.7 18.3l3.5-3.5" stroke="#fff" stroke-width="3" stroke-linecap="round"/>`,
  editor:
    `<rect x="9" y="5" width="30" height="38" rx="3" fill="#fff" stroke="#c8c8c8"/>` +
    `<rect x="9" y="5" width="30" height="8" rx="3" fill="#3a96dd"/><rect x="9" y="10" width="30" height="3" fill="#3a96dd"/>` +
    `<path d="M14 19h20M14 25h20M14 31h14M14 37h17" stroke="#9e9e9e" stroke-width="2" stroke-linecap="round"/>`,
  viewer:
    `${g('vwB', '#5ec7ff', '#1f8ae0')}<rect x="5" y="8" width="38" height="32" rx="5" fill="url(#vwB)"/>` +
    `<circle cx="32" cy="17" r="4" fill="#ffe082"/><path d="M5 34l11-12 9 9 5-5 13 12v2a5 5 0 0 1-5 5H10a5 5 0 0 1-5-5z" fill="#2e7d32"/>` +
    `<path d="M5 37l11-9 9 8 5-4 13 9a5 5 0 0 1-5 4H10a5 5 0 0 1-5-5z" fill="#66bb6a"/>`,
  users:
    `${g('usB', '#3ec1d3', '#0f8fa8')}<rect x="4" y="6" width="40" height="36" rx="6" fill="url(#usB)"/>` +
    `<circle cx="24" cy="19" r="6" fill="#fff"/><path d="M13 36c0-6 5-10 11-10s11 4 11 10z" fill="#fff"/>`,
  audit:
    `${g('auB', '#ffa53d', '#e87500')}<path d="M24 4l17 6v12c0 11-7 18-17 22C14 40 7 33 7 22V10z" fill="url(#auB)"/>` +
    `<path d="M16 18h16M16 24h16M16 30h10" stroke="#fff" stroke-width="3" stroke-linecap="round"/>`,
  settings:
    `${g('stB', '#8a8a8a', '#5c5c5c')}<circle cx="24" cy="24" r="18" fill="url(#stB)"/>` +
    `<path d="M24 12l2.6 3.6 4.4-.9.9 4.4L35.5 22 33 25.6l.9 4.4-4.4.9L26.6 34.5 24 32l-3.6 2.5-2.9-3.6-4.4-.9.9-4.4L12.5 22l3.5-3 .9-4.3 4.4.9z" fill="#e0e0e0"/>` +
    `<circle cx="24" cy="23.5" r="4.5" fill="#6d6d6d"/>`,
  trash:
    `${g('trB', '#e0e0e0', '#b0b0b0')}<path d="M12 14h24l-2.5 27a3 3 0 0 1-3 2.7h-13a3 3 0 0 1-3-2.7z" fill="url(#trB)" stroke="#9e9e9e"/>` +
    `<rect x="9" y="9" width="30" height="5" rx="2" fill="#cfcfcf" stroke="#9e9e9e"/><rect x="19" y="5" width="10" height="4" rx="1.5" fill="#bdbdbd"/>` +
    `<path d="M19 19v19M24 19v19M29 19v19" stroke="#9e9e9e" stroke-width="1.6"/>`,
  trashFull:
    `${g('trF', '#e0e0e0', '#b0b0b0')}<path d="M12 14h24l-2.5 27a3 3 0 0 1-3 2.7h-13a3 3 0 0 1-3-2.7z" fill="url(#trF)" stroke="#9e9e9e"/>` +
    `<path d="M14 14l5-7 6 5 6-6 4 8z" fill="#fff" stroke="#bdbdbd"/><path d="M15 10l8 4" stroke="#3a96dd" stroke-width="2"/>` +
    `<path d="M19 19v19M24 19v19M29 19v19" stroke="#9e9e9e" stroke-width="1.6"/>`,
  network:
    `${g('nwB', '#4fb3ff', '#0067c0')}<circle cx="24" cy="24" r="18" fill="url(#nwB)"/>` +
    `<ellipse cx="24" cy="24" rx="8" ry="18" fill="none" stroke="#fff" stroke-width="2"/><path d="M6 24h36M9 15h30M9 33h30" stroke="#fff" stroke-width="2"/>`,
  about:
    `${g('abB', '#4fb3ff', '#0067c0')}<circle cx="24" cy="24" r="18" fill="url(#abB)"/>` +
    `<circle cx="24" cy="15.5" r="2.6" fill="#fff"/><rect x="21.5" y="21" width="5" height="14" rx="2" fill="#fff"/>`,
  tasks:
    `${g('tkB', '#4f8cff', '#2b5dd8')}<rect x="4" y="6" width="40" height="36" rx="6" fill="url(#tkB)"/>` +
    `<path d="M12 17l3 3 5-6M12 29l3 3 5-6" stroke="#fff" stroke-width="2.6" fill="none" stroke-linecap="round" stroke-linejoin="round"/>` +
    `<path d="M24 17h12M24 29h12" stroke="#fff" stroke-width="2.6" stroke-linecap="round"/>`,
  servers:
    `${g('srB', '#5c6bc0', '#3949ab')}<rect x="7" y="5" width="34" height="11" rx="3" fill="url(#srB)"/>` +
    `<rect x="7" y="18.5" width="34" height="11" rx="3" fill="url(#srB)"/><rect x="7" y="32" width="34" height="11" rx="3" fill="url(#srB)"/>` +
    `<path d="M12 10.5h12M12 24h12M12 37.5h12" stroke="#c5cae9" stroke-width="2" stroke-linecap="round"/>` +
    `<circle cx="35" cy="10.5" r="2" fill="#69f0ae"/><circle cx="35" cy="24" r="2" fill="#69f0ae"/><circle cx="35" cy="37.5" r="2" fill="#ffd740"/>`,
  server:
    `${g('sv1', '#78909c', '#455a64')}<rect x="8" y="6" width="32" height="36" rx="4" fill="url(#sv1)"/>` +
    `<rect x="12" y="11" width="24" height="6" rx="1.5" fill="#cfd8dc"/><rect x="12" y="21" width="24" height="6" rx="1.5" fill="#cfd8dc"/>` +
    `<circle cx="16" cy="35" r="2.2" fill="#69f0ae"/><path d="M22 35h10" stroke="#cfd8dc" stroke-width="2" stroke-linecap="round"/>`,
  traffic:
    `${g('tfB', '#26a69a', '#00796b')}<rect x="4" y="6" width="40" height="36" rx="6" fill="url(#tfB)"/>` +
    `<path d="M17 34V14M11 20l6-6 6 6" stroke="#fff" stroke-width="3" fill="none" stroke-linecap="round" stroke-linejoin="round"/>` +
    `<path d="M31 14v20M25 28l6 6 6-6" stroke="#b2dfdb" stroke-width="3" fill="none" stroke-linecap="round" stroke-linejoin="round"/>`,
  batch:
    `${g('btB', '#3d3d3d', '#1e1e1e')}<rect x="12" y="4" width="32" height="24" rx="4" fill="#616161"/>` +
    `<rect x="8" y="10" width="32" height="24" rx="4" fill="#424242"/><rect x="4" y="16" width="32" height="26" rx="4" fill="url(#btB)"/>` +
    `<path d="M10 25l5 4-5 4" stroke="#4fc3f7" stroke-width="2.6" fill="none" stroke-linecap="round" stroke-linejoin="round"/>` +
    `<path d="M18 35h10" stroke="#d4d4d4" stroke-width="2.6" stroke-linecap="round"/>`,
  probes:
    `${g('pbB', '#ef5350', '#c62828')}<rect x="4" y="6" width="40" height="36" rx="6" fill="url(#pbB)"/>` +
    `<path d="M24 36s-11-6.5-11-14a6 6 0 0 1 11-3.3A6 6 0 0 1 35 22c0 7.5-11 14-11 14z" fill="#fff" opacity=".95"/>` +
    `<path d="M14 24h6l2-4 3 7 2-3h7" stroke="#e53935" stroke-width="2" fill="none" stroke-linecap="round" stroke-linejoin="round"/>`,
}

export const fileIcons: Record<string, string> = {
  folder: folder(),
  file: doc('#9e9e9e', ''),
  text: doc('#607d8b', label('TXT')),
  code: doc('#3a96dd', label('</>')),
  config: doc('#6d4c41', label('CFG')),
  shell: doc('#2e7d32', label('SH')),
  image: doc('#43a047', label('IMG')),
  video: doc('#e53935', label('VID')),
  audio: doc('#8e24aa', label('AUD')),
  archive: doc('#f9a825', label('ZIP')),
  pdf: doc('#d32f2f', label('PDF')),
  log: doc('#78909c', label('LOG')),
  binary: doc('#455a64', label('BIN')),
  disk:
    `${g('dkB', '#e0e0e0', '#bdbdbd')}<rect x="5" y="14" width="38" height="20" rx="4" fill="url(#dkB)" stroke="#9e9e9e"/>` +
    `<rect x="9" y="26" width="18" height="3" rx="1.5" fill="#757575"/><circle cx="36" cy="27.5" r="2.2" fill="#43a047"/>`,
  diskSystem:
    `${g('dsB', '#e0e0e0', '#bdbdbd')}<rect x="5" y="14" width="38" height="20" rx="4" fill="url(#dsB)" stroke="#9e9e9e"/>` +
    `<rect x="9" y="26" width="18" height="3" rx="1.5" fill="#757575"/><circle cx="36" cy="27.5" r="2.2" fill="#43a047"/>` +
    `<g transform="translate(10 6)"><rect width="5" height="5" fill="#f25022"/><rect x="6" width="5" height="5" fill="#7fba00"/><rect y="6" width="5" height="5" fill="#00a4ef"/><rect x="6" y="6" width="5" height="5" fill="#ffb900"/></g>`,
}

const extMap: Record<string, string> = {}
const reg = (kind: string, exts: string) => exts.split(' ').forEach((e) => (extMap[e] = kind))
reg('text', 'txt md markdown rst csv tsv')
reg('code', 'js mjs cjs ts tsx jsx vue json go py rb php java c h cpp hpp cc rs kt swift cs lua pl html htm css scss less xml sql proto')
reg('config', 'conf cfg ini yaml yml toml env service timer socket properties cnf')
reg('shell', 'sh bash zsh fish')
reg('image', 'png jpg jpeg gif webp bmp svg ico avif')
reg('video', 'mp4 webm mkv mov avi m4v')
reg('audio', 'mp3 wav ogg flac m4a aac opus')
reg('archive', 'zip tar gz tgz xz txz bz2 tbz2 7z rar zst')
reg('pdf', 'pdf')
reg('log', 'log out err')
reg('binary', 'bin so o a deb rpm iso img exe')

export function fileKind(name: string, isDir: boolean): string {
  if (isDir) return 'folder'
  const lower = name.toLowerCase()
  if (lower === 'dockerfile' || lower === 'makefile' || lower.startsWith('.')) {
    if (lower === '.bashrc' || lower === '.profile' || lower === '.zshrc') return 'shell'
    return lower === 'dockerfile' || lower === 'makefile' ? 'code' : 'config'
  }
  const ext = lower.includes('.') ? lower.slice(lower.lastIndexOf('.') + 1) : ''
  return extMap[ext] ?? 'file'
}

export function iconSVG(name: string): string {
  const body = appIcons[name] ?? fileIcons[name] ?? fileIcons.file
  return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 48 48">${body}</svg>`
}
