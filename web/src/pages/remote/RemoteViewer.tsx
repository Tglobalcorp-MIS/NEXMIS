import { useCallback, useEffect, useRef, useState, type KeyboardEvent, type MouseEvent } from 'react'
import { useParams } from 'react-router'
import { useQuery } from '@tanstack/react-query'
import { Button, ConfigProvider, Flex, Input, Modal, Popconfirm, Result, Segmented, Select, Slider, Space, Spin, Tag, Tooltip, Typography, theme } from 'antd'
import {
  CompressOutlined,
  DisconnectOutlined,
  ExpandOutlined,
  FullscreenExitOutlined,
  FullscreenOutlined,
  SnippetsOutlined,
  KeyOutlined,
} from '@ant-design/icons'
import { remoteApi } from '../../api/endpoints'
import { wsUrl } from '../../api/client'
import { qk } from '../../api/keys'
import type { RemoteClientMessage, RemoteScreenInfo, RemoteSession, RemoteStatusFrame } from '../../api/types'
import { remoteEndReasonLabel, remoteModeLabels } from '../../utils/labels'
import { showError, showSuccess } from '../../utils/feedback'

type Phase = 'connecting' | 'waiting_consent' | 'active' | 'reconnecting' | 'ended' | 'denied' | 'error'

const FINAL: Phase[] = ['ended', 'denied', 'error']
const RECONNECT_WINDOW_MS = 20_000

const reasonText = (r?: string | null) => remoteEndReasonLabel(r)

// 遠端游標圖示
const cursorSvg =
  'data:image/svg+xml;utf8,' +
  encodeURIComponent(
    '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 20 20"><path d="M2 1l0 15 4-4 3 7 3-1-3-7 6 0z" fill="#fff" stroke="#000" stroke-width="1.2"/></svg>',
  )

export default function RemoteViewer() {
  const { sessionId = '' } = useParams()
  const [phase, setPhase] = useState<Phase>('connecting')
  const [reason, setReason] = useState('')
  const [screen, setScreen] = useState<RemoteScreenInfo | null>(null)
  const [fit, setFit] = useState(true)
  const [quality, setQuality] = useState(60)
  const [fps, setFps] = useState(0)
  const [latency, setLatency] = useState<number | null>(null)
  const [fullscreen, setFullscreen] = useState(false)
  const [pasteOpen, setPasteOpen] = useState(false)
  const [pasteText, setPasteText] = useState('')
  const [connKey, setConnKey] = useState(0)

  const canvasRef = useRef<HTMLCanvasElement>(null)
  const cursorRef = useRef<HTMLDivElement>(null)
  const stageRef = useRef<HTMLDivElement>(null)
  const wsRef = useRef<WebSocket | null>(null)
  const cursorPos = useRef<{ x: number; y: number } | null>(null)
  const pendingMove = useRef<{ x: number; y: number } | null>(null)
  const moveRaf = useRef(0)
  const phaseRef = useRef<Phase>('connecting')

  const { data: session, refetch: refetchSession } = useQuery({
    queryKey: qk.remoteSession(sessionId),
    queryFn: () => remoteApi.get(sessionId),
    refetchInterval: (q) => (q.state.data?.status === 'pending' ? 3000 : false),
  })
  const control = session?.mode === 'control'

  useEffect(() => {
    document.title = session?.device_name ? `遠端：${session.device_name} - NEXMIS` : '遠端檢視 - NEXMIS'
  }, [session?.device_name])

  const updatePhase = useCallback((p: Phase, r = '') => {
    phaseRef.current = p
    setPhase(p)
    setReason(r)
  }, [])

  const send = useCallback((m: RemoteClientMessage) => {
    const ws = wsRef.current
    if (ws && ws.readyState === WebSocket.OPEN) ws.send(JSON.stringify(m))
  }, [])

  /** 依 canvas 顯示大小放置遠端游標 */
  const placeCursor = useCallback(() => {
    const c = canvasRef.current
    const el = cursorRef.current
    const pos = cursorPos.current
    if (!c || !el || !pos || !c.width) return
    const sx = c.clientWidth / c.width
    const sy = c.clientHeight / c.height
    el.style.transform = `translate(${c.offsetLeft + pos.x * sx}px, ${c.offsetTop + pos.y * sy}px)`
    el.style.display = 'block'
  }, [])

  // ---------- WebSocket 媒體通道 ----------
  useEffect(() => {
    if (!sessionId) return
    let stopped = false
    let retryTimer: ReturnType<typeof setTimeout> | undefined
    let disconnectedAt = 0
    let frames = 0
    let ackSentAt = 0
    let lastLatency: number | null = null
    let drawChain: Promise<void> = Promise.resolve()

    const statsTimer = setInterval(() => {
      setFps(frames)
      frames = 0
      setLatency(lastLatency)
    }, 1000)

    const onBinary = (buf: ArrayBuffer) => {
      if (buf.byteLength < 1) return
      const dv = new DataView(buf)
      const type = dv.getUint8(0)
      switch (type) {
        case 0x01: {
          const info = JSON.parse(new TextDecoder().decode(new Uint8Array(buf, 1))) as RemoteScreenInfo
          const c = canvasRef.current
          if (c && (c.width !== info.width || c.height !== info.height)) {
            c.width = info.width
            c.height = info.height
          }
          setScreen(info)
          if (phaseRef.current !== 'active') updatePhase('active')
          break
        }
        case 0x02: {
          if (buf.byteLength < 9) return
          const x = dv.getUint16(1)
          const y = dv.getUint16(3)
          if (ackSentAt) {
            lastLatency = Math.round(performance.now() - ackSentAt)
            ackSentAt = 0
          }
          const bitmap = createImageBitmap(new Blob([new Uint8Array(buf, 9)], { type: 'image/jpeg' }))
          // 依收到順序繪製，避免重疊區塊次序錯亂
          drawChain = drawChain
            .then(() => bitmap)
            .then((bmp) => {
              canvasRef.current?.getContext('2d')?.drawImage(bmp, x, y)
              bmp.close()
            })
            .catch(() => {})
          if (phaseRef.current !== 'active') updatePhase('active')
          break
        }
        case 0x03: {
          if (buf.byteLength < 5) return
          const seq = dv.getUint32(1)
          drawChain = drawChain.then(() => {
            frames++
            ackSentAt = performance.now()
            send({ t: 'ack', seq })
          })
          break
        }
        case 0x04: {
          if (buf.byteLength < 5) return
          cursorPos.current = { x: dv.getUint16(1), y: dv.getUint16(3) }
          placeCursor()
          break
        }
        default:
          break
      }
    }

    const onText = (text: string) => {
      let msg: RemoteStatusFrame
      try {
        msg = JSON.parse(text) as RemoteStatusFrame
      } catch {
        return
      }
      if (msg.t !== 'status') return
      switch (msg.status) {
        case 'waiting_consent':
          updatePhase('waiting_consent')
          break
        case 'active':
          updatePhase('active')
          break
        case 'denied':
          updatePhase('denied', reasonText(msg.reason))
          break
        case 'ended':
          updatePhase('ended', reasonText(msg.reason))
          break
      }
    }

    const finalFromSession = (s: RemoteSession | undefined): boolean => {
      if (!s) return false
      if (s.status === 'denied') updatePhase('denied', reasonText(s.end_reason))
      else if (s.status === 'ended') updatePhase('ended', reasonText(s.end_reason))
      else if (s.status === 'failed') updatePhase('error', reasonText(s.end_reason) || '連線失敗')
      else return false
      return true
    }

    const connect = () => {
      if (stopped) return
      const ws = new WebSocket(wsUrl(`/remote-sessions/${encodeURIComponent(sessionId)}/ws`))
      ws.binaryType = 'arraybuffer'
      wsRef.current = ws
      ws.onopen = () => {
        disconnectedAt = 0
        if (phaseRef.current === 'connecting' || phaseRef.current === 'reconnecting') updatePhase('waiting_consent')
        send({ t: 'quality', q: quality })
      }
      ws.onmessage = (ev) => {
        if (typeof ev.data === 'string') onText(ev.data)
        else if (ev.data instanceof ArrayBuffer) onBinary(ev.data)
      }
      ws.onclose = () => {
        if (wsRef.current === ws) wsRef.current = null
        if (stopped || FINAL.includes(phaseRef.current)) return
        // 伺服器以 HTTP 410 拒絕代表 session 已結束；瀏覽器無法讀到狀態碼，改查 REST
        void refetchSession().then((r) => {
          if (stopped || finalFromSession(r.data)) return
          if (!disconnectedAt) disconnectedAt = Date.now()
          if (Date.now() - disconnectedAt > RECONNECT_WINDOW_MS) {
            updatePhase('error', '連線中斷且無法重新連線')
            return
          }
          updatePhase('reconnecting')
          retryTimer = setTimeout(connect, 2000)
        })
      }
    }

    // 延後一個 tick 再連線，避免 StrictMode 開發模式下重複建立連線
    retryTimer = setTimeout(connect, 0)
    return () => {
      stopped = true
      clearTimeout(retryTimer)
      clearInterval(statsTimer)
      wsRef.current?.close()
      wsRef.current = null
    }
    // quality 只用於初次連線；之後由滑桿傳送
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, connKey, send, updatePhase, placeCursor, refetchSession])

  // 視窗大小或縮放模式改變時重新放置游標
  useEffect(() => {
    const onResize = () => placeCursor()
    window.addEventListener('resize', onResize)
    const t = setTimeout(placeCursor, 50)
    return () => {
      window.removeEventListener('resize', onResize)
      clearTimeout(t)
    }
  }, [fit, placeCursor, screen])

  useEffect(() => {
    const onFs = () => setFullscreen(!!document.fullscreenElement)
    document.addEventListener('fullscreenchange', onFs)
    return () => document.removeEventListener('fullscreenchange', onFs)
  }, [])

  // ---------- 輸入（control 模式） ----------
  const inputEnabled = control && phase === 'active'

  const toRemote = useCallback((clientX: number, clientY: number) => {
    const c = canvasRef.current
    if (!c || !c.width) return null
    const r = c.getBoundingClientRect()
    const x = Math.round(((clientX - r.left) * c.width) / r.width)
    const y = Math.round(((clientY - r.top) * c.height) / r.height)
    return { x: Math.max(0, Math.min(c.width - 1, x)), y: Math.max(0, Math.min(c.height - 1, y)) }
  }, [])

  const onMouseMove = (e: MouseEvent<HTMLCanvasElement>) => {
    if (!inputEnabled) return
    pendingMove.current = toRemote(e.clientX, e.clientY)
    if (moveRaf.current) return
    moveRaf.current = requestAnimationFrame(() => {
      moveRaf.current = 0
      const p = pendingMove.current
      if (p) send({ t: 'mouse', a: 'move', x: p.x, y: p.y })
    })
  }

  const onMouseButton = (a: 'down' | 'up', e: MouseEvent<HTMLCanvasElement>) => {
    if (!inputEnabled) return
    e.preventDefault()
    if (a === 'down') canvasRef.current?.focus()
    const p = toRemote(e.clientX, e.clientY)
    if (p) send({ t: 'mouse', a, x: p.x, y: p.y, b: e.button })
  }

  // 滾輪需 passive: false 才能 preventDefault
  useEffect(() => {
    const c = canvasRef.current
    if (!c || !inputEnabled) return
    const onWheel = (e: WheelEvent) => {
      e.preventDefault()
      const p = toRemote(e.clientX, e.clientY)
      if (!p) return
      const scale = e.deltaMode === 1 ? 40 : e.deltaMode === 2 ? 800 : 1.2
      const d = Math.max(-1200, Math.min(1200, Math.round(-e.deltaY * scale)))
      if (d) send({ t: 'mouse', a: 'wheel', x: p.x, y: p.y, d })
    }
    c.addEventListener('wheel', onWheel, { passive: false })
    return () => c.removeEventListener('wheel', onWheel)
  }, [inputEnabled, send, toRemote])

  const onKey = (a: 'down' | 'up', e: KeyboardEvent<HTMLCanvasElement>) => {
    if (!inputEnabled) return
    e.preventDefault()
    e.stopPropagation()
    if (a === 'down' && e.repeat && e.code.startsWith('Shift')) return
    send({ t: 'key', a, code: e.code, key: e.key })
  }

  // ---------- 工具列動作 ----------
  const stop = async () => {
    try {
      await remoteApi.stop(sessionId)
    } catch (e) {
      if (e instanceof Error) showError(e.message)
    }
    updatePhase('ended', '您已結束連線')
    wsRef.current?.close()
  }

  const toggleFullscreen = () => {
    if (document.fullscreenElement) void document.exitFullscreen()
    else void document.documentElement.requestFullscreen().catch(() => {})
  }

  const openPaste = async () => {
    let t = ''
    try {
      t = await navigator.clipboard.readText()
    } catch {
      /* 無剪貼簿權限時手動輸入 */
    }
    setPasteText(t)
    setPasteOpen(true)
  }

  const sendPaste = () => {
    if (pasteText) {
      send({ t: 'text', s: pasteText })
      showSuccess('已傳送文字')
    }
    setPasteOpen(false)
    canvasRef.current?.focus()
  }

  const reconnect = () => {
    updatePhase('connecting')
    setConnKey((k) => k + 1)
  }

  const statusTag =
    phase === 'active' ? (
      <Tag color="green">連線中</Tag>
    ) : phase === 'waiting_consent' ? (
      <Tag color="processing">等待同意</Tag>
    ) : phase === 'reconnecting' || phase === 'connecting' ? (
      <Tag color="warning">連線中…</Tag>
    ) : (
      <Tag>已結束</Tag>
    )

  return (
    <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
      <Flex vertical style={{ height: '100vh', background: '#000', color: '#fff' }}>
        <Flex
          align="center"
          justify="space-between"
          gap={12}
          wrap
          style={{ padding: '6px 12px', background: '#141414', borderBottom: '1px solid #303030', minHeight: 48 }}
        >
          <Space size={8} wrap>
            <img src="/favicon.svg" width={22} height={22} alt="" />
            <Typography.Text strong>{session?.device_name || '遠端檢視'}</Typography.Text>
            {session && <Tag color={control ? 'volcano' : 'geekblue'}>{remoteModeLabels[session.mode]}</Tag>}
            {statusTag}
            {session?.consent_user && <Typography.Text type="secondary">同意者：{session.consent_user}</Typography.Text>}
          </Space>
          <Space size={8} wrap>
            <Typography.Text type="secondary" style={{ fontVariantNumeric: 'tabular-nums', fontSize: 12 }}>
              {fps} FPS · 延遲 {latency === null ? '-' : `${latency} ms`}
              {screen ? ` · ${screen.width}×${screen.height}` : ''}
            </Typography.Text>
            <Segmented
              size="small"
              value={fit ? 'fit' : 'orig'}
              onChange={(v) => setFit(v === 'fit')}
              options={[
                { value: 'fit', icon: <CompressOutlined />, label: '適應' },
                { value: 'orig', icon: <ExpandOutlined />, label: '原始' },
              ]}
            />
            <Tooltip title="畫質">
              <Flex align="center" gap={6} style={{ width: 150 }}>
                <Typography.Text type="secondary" style={{ fontSize: 12, whiteSpace: 'nowrap' }}>
                  畫質
                </Typography.Text>
                <Slider
                  min={10}
                  max={90}
                  step={10}
                  value={quality}
                  onChange={setQuality}
                  onChangeComplete={(q) => send({ t: 'quality', q })}
                  style={{ flex: 1, margin: '0 4px' }}
                />
              </Flex>
            </Tooltip>
            {screen && screen.monitors > 1 && (
              <Select
                size="small"
                value={screen.monitor}
                onChange={(i: number) => send({ t: 'monitor', i })}
                options={Array.from({ length: screen.monitors }, (_, i) => ({ value: i, label: `螢幕 ${i + 1}` }))}
                style={{ width: 96 }}
              />
            )}
            {control && (
              <>
                <Popconfirm title="傳送 Ctrl+Alt+Del 至遠端？" onConfirm={() => send({ t: 'cad' })} disabled={phase !== 'active'}>
                  <Button size="small" icon={<KeyOutlined />} disabled={phase !== 'active'}>
                    Ctrl+Alt+Del
                  </Button>
                </Popconfirm>
                <Button size="small" icon={<SnippetsOutlined />} disabled={phase !== 'active'} onClick={() => void openPaste()}>
                  貼上文字
                </Button>
              </>
            )}
            <Tooltip title={fullscreen ? '離開全螢幕' : '全螢幕'}>
              <Button size="small" icon={fullscreen ? <FullscreenExitOutlined /> : <FullscreenOutlined />} onClick={toggleFullscreen} />
            </Tooltip>
            {!FINAL.includes(phase) && (
              <Popconfirm title="結束此遠端連線？" okText="結束" okButtonProps={{ danger: true }} onConfirm={() => stop()}>
                <Button size="small" danger type="primary" icon={<DisconnectOutlined />}>
                  結束連線
                </Button>
              </Popconfirm>
            )}
          </Space>
        </Flex>

        <div
          ref={stageRef}
          style={{
            flex: 1,
            position: 'relative',
            overflow: fit ? 'hidden' : 'auto',
            display: 'flex',
            alignItems: fit ? 'center' : 'flex-start',
            justifyContent: fit ? 'center' : 'flex-start',
          }}
        >
          <canvas
            ref={canvasRef}
            tabIndex={0}
            width={0}
            height={0}
            onMouseMove={onMouseMove}
            onMouseDown={(e) => onMouseButton('down', e)}
            onMouseUp={(e) => onMouseButton('up', e)}
            onContextMenu={(e) => inputEnabled && e.preventDefault()}
            onKeyDown={(e) => onKey('down', e)}
            onKeyUp={(e) => onKey('up', e)}
            style={{
              display: phase === 'active' || phase === 'reconnecting' ? 'block' : 'none',
              outline: 'none',
              flexShrink: 0,
              cursor: inputEnabled ? 'default' : 'not-allowed',
              ...(fit ? { maxWidth: '100%', maxHeight: '100%', width: 'auto', height: 'auto', objectFit: 'contain' } : {}),
            }}
          />
          {!control && (
            <div
              ref={cursorRef}
              style={{
                position: 'absolute',
                left: 0,
                top: 0,
                width: 20,
                height: 20,
                pointerEvents: 'none',
                display: 'none',
                background: `url("${cursorSvg}") no-repeat`,
              }}
            />
          )}

          {phase !== 'active' && (
            <Flex align="center" justify="center" style={{ position: 'absolute', inset: 0, background: phase === 'reconnecting' ? 'rgba(0,0,0,0.6)' : '#000' }}>
              {(phase === 'connecting' || phase === 'reconnecting') && <Spin size="large" description={phase === 'reconnecting' ? '連線中斷，正在重新連線…' : '正在建立連線…'}><div style={{ width: 200, height: 80 }} /></Spin>}
              {phase === 'waiting_consent' && (
                <Result
                  icon={<Spin size="large" />}
                  title="等待使用者同意…"
                  subTitle={
                    <>
                      使用者畫面已顯示連線請求（{session ? remoteModeLabels[session.mode] : ''}）。
                      <br />
                      原因：{session?.reason || '-'}
                    </>
                  }
                  extra={
                    <Button danger onClick={() => void stop()}>
                      取消請求
                    </Button>
                  }
                />
              )}
              {phase === 'denied' && (
                <Result status="warning" title="連線請求未獲同意" subTitle={reason || '使用者拒絕了連線請求'} extra={<Button onClick={() => window.close()}>關閉視窗</Button>} />
              )}
              {phase === 'ended' && (
                <Result status="info" title="連線已結束" subTitle={reason} extra={<Button onClick={() => window.close()}>關閉視窗</Button>} />
              )}
              {phase === 'error' && (
                <Result
                  status="error"
                  title="連線失敗"
                  subTitle={reason}
                  extra={
                    <Space>
                      <Button type="primary" onClick={reconnect}>
                        重試
                      </Button>
                      <Button onClick={() => window.close()}>關閉視窗</Button>
                    </Space>
                  }
                />
              )}
            </Flex>
          )}
        </div>
      </Flex>

      <Modal title="貼上文字至遠端" open={pasteOpen} onCancel={() => setPasteOpen(false)} onOk={sendPaste} okText="傳送" destroyOnHidden>
        <Typography.Paragraph type="secondary">文字會以鍵盤輸入的方式送至遠端目前的游標位置。</Typography.Paragraph>
        <Input.TextArea value={pasteText} onChange={(e) => setPasteText(e.target.value)} rows={6} autoFocus maxLength={10000} showCount />
      </Modal>
    </ConfigProvider>
  )
}
