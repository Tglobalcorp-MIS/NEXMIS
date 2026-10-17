import { useCallback, useEffect, useEffectEvent, useMemo, useRef, useState, type ReactNode } from 'react'
import { useQueryClient, type QueryKey } from '@tanstack/react-query'
import { useNavigate } from 'react-router'
import { Button } from 'antd'
import { MessageOutlined } from '@ant-design/icons'
import { wsUrl } from '../api/client'
import { qk } from '../api/keys'
import type { ChatMessage, Device, DeviceDetail, RemoteSession, ServerEvent } from '../api/types'
import { EventsContext, type EventsState } from '../hooks/useEvents'
import { getNotification } from '../utils/feedback'

const MAX_BACKOFF_MS = 30_000
const FLUSH_MS = 1_500

/** 連線事件 WebSocket，依事件 invalidate react-query 快取，並管理未讀訊息。 */
export function EventsProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const navigate = useNavigate()
  const [connected, setConnected] = useState(false)
  const [unread, setUnread] = useState<EventsState['unread']>({})
  const activeChat = useRef<string | null>(null)

  const deviceName = useCallback(
    (id: string): string | undefined => {
      const detail = qc.getQueryData<DeviceDetail>(qk.deviceDetail(id))
      if (detail) return detail.name
      for (const [, data] of qc.getQueriesData<{ items: Device[] }>({ queryKey: qk.devices })) {
        const d = data?.items?.find((x) => x.id === id)
        if (d) return d.name
      }
      return undefined
    },
    [qc],
  )

  const onChat = useEffectEvent((m: ChatMessage) => {
    // 立即寫入快取，之後再由 invalidate 校正
    qc.setQueryData<ChatMessage[]>(qk.deviceSub(m.device_id, 'messages'), (old) => {
      if (!old) return old
      const idx = old.findIndex((x) => x.id === m.id)
      if (idx >= 0) {
        const copy = old.slice()
        copy[idx] = m
        return copy
      }
      return [...old, m]
    })
    if (m.direction !== 'user' || activeChat.current === m.device_id) return
    const name = deviceName(m.device_id) ?? m.sender ?? '裝置'
    setUnread((u) => ({ ...u, [m.device_id]: { count: (u[m.device_id]?.count ?? 0) + 1, name } }))
    const key = `chat-${m.device_id}`
    getNotification()?.open({
      key,
      icon: <MessageOutlined style={{ color: '#1677ff' }} />,
      title: `${name} 的新訊息`,
      description: (
        <div>
          <div style={{ whiteSpace: 'pre-wrap', maxHeight: 120, overflow: 'hidden' }}>
            {m.sender ? `${m.sender}：` : ''}
            {m.body}
          </div>
        </div>
      ),
      actions: (
        <Button
          type="primary"
          size="small"
          onClick={() => {
            getNotification()?.destroy(key)
            void navigate(`/devices/${m.device_id}?tab=chat`)
          }}
        >
          回覆
        </Button>
      ),
      duration: 8,
    })
  })

  useEffect(() => {
    let ws: WebSocket | null = null
    let attempt = 0
    let reconnectTimer: ReturnType<typeof setTimeout> | undefined
    let flushTimer: ReturnType<typeof setTimeout> | undefined
    let stopped = false
    let everConnected = false
    const pending = new Map<string, QueryKey>()

    const flush = () => {
      flushTimer = undefined
      pending.forEach((key) => void qc.invalidateQueries({ queryKey: key }))
      pending.clear()
    }
    const invalidate = (key: QueryKey, immediate = false) => {
      if (immediate) {
        void qc.invalidateQueries({ queryKey: key })
        return
      }
      pending.set(JSON.stringify(key), key)
      flushTimer ??= setTimeout(flush, FLUSH_MS)
    }

    const handle = (ev: ServerEvent) => {
      switch (ev.type) {
        case 'device.online':
        case 'device.offline': {
          const online = ev.type === 'device.online'
          qc.setQueryData<DeviceDetail>(qk.deviceDetail(ev.data.device_id), (old) => (old ? { ...old, online } : old))
          invalidate(qk.devices)
          invalidate(qk.dashboard)
          invalidate(qk.deviceDetail(ev.data.device_id))
          break
        }
        case 'device.updated':
          qc.setQueryData<DeviceDetail>(qk.deviceDetail(ev.data.id), (old) => (old ? { ...old, ...ev.data } : old))
          invalidate(qk.devices)
          invalidate(qk.dashboard)
          break
        case 'command.updated':
          qc.setQueryData(['command', ev.data.id], ev.data)
          invalidate(qk.commands)
          invalidate(qk.deviceSub(ev.data.device_id, 'commands'), true)
          invalidate(qk.dashboard)
          if (ev.data.deployment_id) invalidate(qk.deployment(ev.data.deployment_id))
          break
        case 'chat.message':
          onChat(ev.data)
          invalidate(qk.deviceSub(ev.data.device_id, 'messages'))
          break
        case 'remote.updated':
          qc.setQueryData<RemoteSession>(qk.remoteSession(ev.data.id), ev.data)
          invalidate(qk.remoteSessions, true)
          break
        case 'deployment.updated':
          invalidate(qk.deployments)
          invalidate(qk.deployment(ev.data.id), true)
          break
        case 'scan.updated':
          invalidate(qk.scans)
          invalidate(qk.scan(ev.data.id), true)
          break
        default:
          break
      }
    }

    const connect = () => {
      if (stopped) return
      const sock = new WebSocket(wsUrl('/events/ws'))
      ws = sock
      sock.onopen = () => {
        attempt = 0
        setConnected(true)
        // 斷線期間可能漏掉事件：重新連線後全面更新
        if (everConnected) void qc.invalidateQueries()
        everConnected = true
      }
      sock.onmessage = (msg) => {
        if (typeof msg.data !== 'string') return
        try {
          handle(JSON.parse(msg.data) as ServerEvent)
        } catch {
          /* 忽略無法解析的訊息 */
        }
      }
      sock.onerror = () => sock.close()
      sock.onclose = () => {
        if (ws === sock) ws = null
        setConnected(false)
        if (stopped) return
        const base = Math.min(MAX_BACKOFF_MS, 1000 * 2 ** attempt)
        const delay = base * (0.8 + Math.random() * 0.4)
        attempt++
        reconnectTimer = setTimeout(connect, delay)
      }
    }

    // 延後一個 tick 再連線，避免 StrictMode 開發模式下重複建立連線
    reconnectTimer = setTimeout(connect, 0)
    return () => {
      stopped = true
      clearTimeout(reconnectTimer)
      clearTimeout(flushTimer)
      ws?.close()
    }
  }, [qc])

  const clearUnread = useCallback((deviceId: string) => {
    setUnread((u) => {
      if (!u[deviceId]) return u
      const copy = { ...u }
      delete copy[deviceId]
      return copy
    })
  }, [])

  const setActiveChat = useCallback(
    (deviceId: string | null) => {
      activeChat.current = deviceId
      if (deviceId) clearUnread(deviceId)
    },
    [clearUnread],
  )

  const value = useMemo<EventsState>(
    () => ({
      connected,
      unread,
      totalUnread: Object.values(unread).reduce((a, b) => a + b.count, 0),
      clearUnread,
      setActiveChat,
    }),
    [connected, unread, clearUnread, setActiveChat],
  )

  return <EventsContext.Provider value={value}>{children}</EventsContext.Provider>
}
