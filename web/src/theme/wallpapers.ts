export interface Wallpaper {
  id: string
  name: string
  light: string
  dark: string
}

export const wallpapers: Wallpaper[] = [
  { id: 'bloom', name: '绽放', light: 'url(/wallpapers/bloom-light.svg)', dark: 'url(/wallpapers/bloom-dark.svg)' },
  { id: 'glow', name: '微光', light: 'url(/wallpapers/glow-light.svg)', dark: 'url(/wallpapers/glow-dark.svg)' },
  { id: 'hills', name: '山丘', light: 'url(/wallpapers/hills-light.svg)', dark: 'url(/wallpapers/hills-dark.svg)' },
  { id: 'blue', name: '纯色：蓝', light: 'linear-gradient(#0063b1, #0063b1)', dark: 'linear-gradient(#0b2a4a, #0b2a4a)' },
  { id: 'slate', name: '纯色：灰', light: 'linear-gradient(#4a5459, #4a5459)', dark: 'linear-gradient(#1f2326, #1f2326)' },
]

export function wallpaperCSS(id: string, dark: boolean): string {
  const w = wallpapers.find((x) => x.id === id) ?? wallpapers[0]
  return dark ? w.dark : w.light
}
