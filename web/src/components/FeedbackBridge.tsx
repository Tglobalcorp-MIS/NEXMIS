import { useEffect } from 'react'
import { App } from 'antd'
import { registerFeedback } from '../utils/feedback'

export function FeedbackBridge() {
  const { message, notification } = App.useApp()
  useEffect(() => {
    registerFeedback(message, notification)
  }, [message, notification])
  return null
}
