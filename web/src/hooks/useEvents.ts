import { createContext, useContext } from 'react'

export interface EventsState {
  /** 事件 WebSocket 是否已連線 */
  connected: boolean
  /** 各裝置未讀訊息數 */
  unread: Record<string, { count: number; name: string }>
  totalUnread: number
  clearUnread: (deviceId: string) => void
  /** 目前畫面上開啟聊天的裝置（不顯示通知、不累計未讀） */
  setActiveChat: (deviceId: string | null) => void
}

export const EventsContext = createContext<EventsState>({
  connected: false,
  unread: {},
  totalUnread: 0,
  clearUnread: () => {},
  setActiveChat: () => {},
})

export function useEvents() {
  return useContext(EventsContext)
}
