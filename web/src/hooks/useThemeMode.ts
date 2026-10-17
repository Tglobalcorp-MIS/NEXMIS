import { createContext, useContext } from 'react'

export type ThemeMode = 'light' | 'dark'

export interface ThemeModeState {
  mode: ThemeMode
  toggle: () => void
}

export const ThemeModeContext = createContext<ThemeModeState>({ mode: 'light', toggle: () => {} })

export const THEME_STORAGE_KEY = 'nexmis.theme'

export function readStoredTheme(): ThemeMode {
  try {
    const v = localStorage.getItem(THEME_STORAGE_KEY)
    if (v === 'dark' || v === 'light') return v
  } catch {
    /* localStorage 不可用 */
  }
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export function useThemeMode() {
  return useContext(ThemeModeContext)
}
