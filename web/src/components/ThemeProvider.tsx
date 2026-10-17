import { useCallback, useEffect, useMemo, useState, type ReactNode } from 'react'
import { App as AntApp, ConfigProvider, theme } from 'antd'
import zhTW from 'antd/locale/zh_TW'
import { readStoredTheme, THEME_STORAGE_KEY, ThemeModeContext, type ThemeMode } from '../hooks/useThemeMode'
import { FeedbackBridge } from './FeedbackBridge'

export function ThemeProvider({ children }: { children: ReactNode }) {
  const [mode, setMode] = useState<ThemeMode>(readStoredTheme)

  useEffect(() => {
    try {
      localStorage.setItem(THEME_STORAGE_KEY, mode)
    } catch {
      /* ignore */
    }
    document.documentElement.dataset.theme = mode
    document.documentElement.style.colorScheme = mode
  }, [mode])

  const toggle = useCallback(() => setMode((m) => (m === 'dark' ? 'light' : 'dark')), [])
  const ctx = useMemo(() => ({ mode, toggle }), [mode, toggle])

  return (
    <ThemeModeContext.Provider value={ctx}>
      <ConfigProvider
        locale={zhTW}
        theme={{
          algorithm: mode === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm,
          token: { colorPrimary: '#1677ff', borderRadius: 6 },
          components: { Layout: mode === 'dark' ? {} : { headerBg: '#ffffff', siderBg: '#001529' } },
        }}
      >
        <AntApp style={{ height: '100%' }}>
          <FeedbackBridge />
          {children}
        </AntApp>
      </ConfigProvider>
    </ThemeModeContext.Provider>
  )
}
