import { useEffect, useLayoutEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Avatar, Button, Card, Empty, Flex, Input, Spin, Tooltip, Typography, theme } from 'antd'
import { CheckOutlined, ClockCircleOutlined, SendOutlined, UserOutlined, CustomerServiceOutlined } from '@ant-design/icons'
import dayjs from 'dayjs'
import { deviceApi } from '../../../api/endpoints'
import { qk } from '../../../api/keys'
import type { ChatMessage, DeviceDetail } from '../../../api/types'
import { useEvents } from '../../../hooks/useEvents'
import { fmtTime } from '../../../utils/format'

function sortMessages(list: ChatMessage[]) {
  const map = new Map<string, ChatMessage>()
  list.forEach((m) => map.set(m.id, m))
  return [...map.values()].sort((a, b) => dayjs(a.created_at).valueOf() - dayjs(b.created_at).valueOf())
}

function DeliveryStatus({ m }: { m: ChatMessage }) {
  if (m.read_at)
    return (
      <Tooltip title={`已讀 ${fmtTime(m.read_at, true)}`}>
        <span style={{ color: '#52c41a' }}>
          <CheckOutlined />
          <CheckOutlined style={{ marginLeft: -6 }} /> 已讀
        </span>
      </Tooltip>
    )
  if (m.delivered_at)
    return (
      <Tooltip title={`已送達 ${fmtTime(m.delivered_at, true)}`}>
        <span>
          <CheckOutlined /> 已送達
        </span>
      </Tooltip>
    )
  return (
    <span>
      <ClockCircleOutlined /> 等待送達
    </span>
  )
}

export default function ChatTab({ device }: { device: DeviceDetail }) {
  const { token } = theme.useToken()
  const qc = useQueryClient()
  const { setActiveChat } = useEvents()
  const [text, setText] = useState('')
  const [older, setOlder] = useState<ChatMessage[]>([])
  const [noMore, setNoMore] = useState(false)
  const [loadingOlder, setLoadingOlder] = useState(false)
  const scrollRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)

  const { data, isLoading } = useQuery({
    queryKey: qk.deviceSub(device.id, 'messages'),
    queryFn: () => deviceApi.messages(device.id),
  })

  const messages = useMemo(() => sortMessages([...older, ...(data ?? [])]), [older, data])

  // 標記目前開啟的聊天，避免重複通知
  useEffect(() => {
    setActiveChat(device.id)
    return () => setActiveChat(null)
  }, [device.id, setActiveChat])

  // 有未讀的使用者訊息時標為已讀
  const unreadCount = messages.filter((m) => m.direction === 'user' && !m.read_at).length
  useEffect(() => {
    if (!unreadCount) return
    const t = setTimeout(() => {
      deviceApi
        .markRead(device.id)
        .then(() => qc.invalidateQueries({ queryKey: qk.deviceSub(device.id, 'messages') }))
        .catch(() => {})
    }, 500)
    return () => clearTimeout(t)
  }, [unreadCount, device.id, qc])

  // 新訊息時捲到底部（若使用者原本就在底部）
  useLayoutEffect(() => {
    const el = scrollRef.current
    if (el && stickBottom.current) el.scrollTop = el.scrollHeight
  }, [messages.length])

  const send = useMutation({
    mutationFn: (body: string) => deviceApi.sendMessage(device.id, body),
    onSuccess: (m) => {
      setText('')
      stickBottom.current = true
      qc.setQueryData<ChatMessage[]>(qk.deviceSub(device.id, 'messages'), (old) => sortMessages([...(old ?? []), m]))
    },
  })

  const submit = () => {
    const body = text.trim()
    if (!body || send.isPending) return
    send.mutate(body)
  }

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      submit()
    }
  }

  const loadOlder = async () => {
    const oldest = messages[0]
    if (!oldest) return
    setLoadingOlder(true)
    stickBottom.current = false
    try {
      const list = await deviceApi.messages(device.id, oldest.created_at)
      if (!list.length) setNoMore(true)
      setOlder((o) => [...list, ...o])
    } finally {
      setLoadingOlder(false)
    }
  }

  return (
    <Card styles={{ body: { padding: 0 } }}>
      {!device.helper_online && (
        <Alert
          type="info"
          showIcon
          banner
          title={device.online ? '使用者端助手未執行（可能無人登入），訊息將於使用者登入後送達。' : '裝置目前離線，訊息將於上線後送達。'}
        />
      )}
      <div
        ref={scrollRef}
        className="nx-chat-scroll"
        onScroll={(e) => {
          const el = e.currentTarget
          stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
        }}
        style={{ height: 'calc(100vh - 400px)', minHeight: 320, overflow: 'auto', padding: 16, background: token.colorFillQuaternary }}
      >
        {isLoading ? (
          <Flex justify="center" style={{ paddingTop: 40 }}>
            <Spin />
          </Flex>
        ) : messages.length === 0 ? (
          <Empty description="尚無訊息，傳送第一則訊息給使用者吧" style={{ marginTop: 60 }} />
        ) : (
          <>
            {!noMore && messages.length >= 50 && (
              <Flex justify="center" style={{ marginBottom: 12 }}>
                <Button size="small" type="link" loading={loadingOlder} onClick={() => void loadOlder()}>
                  載入較早的訊息
                </Button>
              </Flex>
            )}
            {messages.map((m, i) => {
              const mine = m.direction === 'mis'
              const prev = messages[i - 1]
              const showDate = !prev || !dayjs(prev.created_at).isSame(m.created_at, 'day')
              return (
                <div key={m.id}>
                  {showDate && (
                    <Flex justify="center" style={{ margin: '12px 0' }}>
                      <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                        {dayjs(m.created_at).format('YYYY年M月D日 dddd')}
                      </Typography.Text>
                    </Flex>
                  )}
                  <Flex justify={mine ? 'flex-end' : 'flex-start'} gap={8} style={{ marginBottom: 12 }}>
                    {!mine && <Avatar icon={<UserOutlined />} style={{ flexShrink: 0 }} />}
                    <Flex vertical align={mine ? 'flex-end' : 'flex-start'} style={{ maxWidth: '70%' }}>
                      <Typography.Text type="secondary" style={{ fontSize: 12, marginBottom: 2 }}>
                        {m.sender || (mine ? 'MIS' : '使用者')} · {dayjs(m.created_at).format('HH:mm')}
                      </Typography.Text>
                      <div
                        style={{
                          background: mine ? token.colorPrimary : token.colorBgContainer,
                          color: mine ? '#fff' : token.colorText,
                          padding: '8px 12px',
                          borderRadius: 12,
                          borderTopRightRadius: mine ? 2 : 12,
                          borderTopLeftRadius: mine ? 12 : 2,
                          whiteSpace: 'pre-wrap',
                          wordBreak: 'break-word',
                          boxShadow: token.boxShadowTertiary,
                        }}
                      >
                        {m.body}
                      </div>
                      {mine && (
                        <Typography.Text type="secondary" style={{ fontSize: 11, marginTop: 2 }}>
                          <DeliveryStatus m={m} />
                        </Typography.Text>
                      )}
                    </Flex>
                    {mine && <Avatar icon={<CustomerServiceOutlined />} style={{ background: token.colorPrimary, flexShrink: 0 }} />}
                  </Flex>
                </div>
              )
            })}
          </>
        )}
      </div>
      <Flex gap={8} style={{ padding: 12, borderTop: `1px solid ${token.colorBorderSecondary}` }} align="flex-end">
        <Input.TextArea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={onKeyDown}
          autoSize={{ minRows: 1, maxRows: 5 }}
          placeholder="輸入訊息，Enter 傳送、Shift+Enter 換行"
          maxLength={4000}
        />
        <Button type="primary" icon={<SendOutlined />} onClick={submit} loading={send.isPending} disabled={!text.trim()}>
          傳送
        </Button>
      </Flex>
    </Card>
  )
}
