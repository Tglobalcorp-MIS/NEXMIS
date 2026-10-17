import { useMemo } from 'react'
import { theme } from 'antd'
import { useThemeMode } from './useThemeMode'

// 參考 dataviz 色盤：類別色依固定順序指派、深淺模式各自一組；狀態色保留給健康狀態。
const categoricalLight = ['#2a78d6', '#eb6834', '#1baf7a', '#eda100', '#e87ba4', '#008300', '#6250d6', '#e34948']
const categoricalDark = ['#3987e5', '#d95926', '#199e70', '#c98500', '#d55181', '#008300', '#9085e9', '#e66767']

export const statusColors = {
  good: '#0ca30c',
  warning: '#fab219',
  serious: '#ec835a',
  critical: '#d03b3b',
  unknown: '#a3a3a3',
}

export function useChartTheme() {
  const { mode } = useThemeMode()
  const { token } = theme.useToken()
  return useMemo(
    () => ({
      series: mode === 'dark' ? categoricalDark : categoricalLight,
      text: token.colorTextSecondary,
      textPrimary: token.colorText,
      grid: token.colorBorderSecondary,
      surface: token.colorBgContainer,
      tooltipBg: token.colorBgElevated,
      tooltipBorder: token.colorBorder,
    }),
    [mode, token],
  )
}

