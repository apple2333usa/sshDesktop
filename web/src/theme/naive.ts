// Naive UI 主题覆盖：按 Windows 11（Fluent）风格调整圆角、字体和强调色。
import type { GlobalThemeOverrides } from 'naive-ui'

const font =
  "'Segoe UI Variable Text', 'Segoe UI', 'Microsoft YaHei UI', 'PingFang SC', system-ui, -apple-system, 'Noto Sans CJK SC', sans-serif"
const mono = "'Cascadia Mono', 'Cascadia Code', Consolas, 'JetBrains Mono', monospace"

export function themeOverrides(dark: boolean): GlobalThemeOverrides {
  const accent = dark ? '#4cc2ff' : '#005fb8'
  const accentHover = dark ? '#47b1e8' : '#1a6fc2'
  const accentPressed = dark ? '#42a1d2' : '#0a5ba8'
  return {
    common: {
      fontFamily: font,
      fontFamilyMono: mono,
      fontSize: '14px',
      fontSizeMedium: '14px',
      borderRadius: '4px',
      borderRadiusSmall: '4px',
      primaryColor: accent,
      primaryColorHover: accentHover,
      primaryColorPressed: accentPressed,
      primaryColorSuppl: accent,
      errorColor: dark ? '#ff99a4' : '#c42b1c',
      warningColor: dark ? '#fce100' : '#9d5d00',
      successColor: dark ? '#6ccb5f' : '#0f7b0f',
      ...(dark
        ? { bodyColor: '#202020', cardColor: '#2b2b2b', modalColor: '#2b2b2b', popoverColor: '#2c2c2c' }
        : { bodyColor: '#f3f3f3', cardColor: '#fbfbfb', modalColor: '#fbfbfb', popoverColor: '#f9f9f9' }),
    },
    Button: { textColorPrimary: dark ? '#000' : '#fff', fontWeight: '400' },
    Card: { borderRadius: '8px' },
    Dialog: { borderRadius: '8px' },
    Modal: {},
    DataTable: { thPaddingSmall: '6px 8px', tdPaddingSmall: '5px 8px' },
    Menu: { borderRadius: '4px', itemHeight: '36px' },
  }
}
