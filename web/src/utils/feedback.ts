import type { MessageInstance } from 'antd/es/message/interface'
import type { NotificationInstance } from 'antd/es/notification/interface'

// antd 的 message / notification 需透過 <App> context 取得才能套用主題，
// 由 FeedbackBridge 在掛載時註冊，供非 React 模組（API 用戶端、react-query）使用。
let messageApi: MessageInstance | null = null
let notificationApi: NotificationInstance | null = null

export function registerFeedback(m: MessageInstance, n: NotificationInstance) {
  messageApi = m
  notificationApi = n
}

export function showError(text: string) {
  if (messageApi) void messageApi.error(text)
  else console.error(text)
}

export function showSuccess(text: string) {
  if (messageApi) void messageApi.success(text)
}

export function getNotification(): NotificationInstance | null {
  return notificationApi
}
