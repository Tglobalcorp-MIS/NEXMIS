import { lazy, Suspense } from 'react'
import { useNavigate, useParams, useSearchParams } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Badge, Button, Flex, Popconfirm, Result, Skeleton, Space, Tabs, Tag, Typography } from 'antd'
import { ArrowLeftOutlined, DeleteOutlined, FolderOutlined, ReloadOutlined } from '@ant-design/icons'
import { deviceApi } from '../../api/endpoints'
import { ApiError } from '../../api/client'
import { qk } from '../../api/keys'
import { PageHeader } from '../../components/PageHeader'
import { HealthTag, LifecycleTag, OnlineStatus } from '../../components/StatusTags'
import { FullPageSpin } from '../../components/FullPageSpin'
import { useAuth } from '../../hooks/useAuth'
import { useEvents } from '../../hooks/useEvents'
import { deviceTypeLabels, labelOf } from '../../utils/labels'
import { showSuccess } from '../../utils/feedback'
import OverviewTab from './tabs/OverviewTab'

const HardwareTab = lazy(() => import('./tabs/HardwareTab'))
const SoftwareTab = lazy(() => import('./tabs/SoftwareTab'))
const HealthTab = lazy(() => import('./tabs/HealthTab'))
const BatteryTab = lazy(() => import('./tabs/BatteryTab'))
const CommandsTab = lazy(() => import('./tabs/CommandsTab'))
const ChatTab = lazy(() => import('./tabs/ChatTab'))
const RemoteTab = lazy(() => import('./tabs/RemoteTab'))

export default function DeviceDetail() {
  const { id = '' } = useParams()
  const [sp, setSp] = useSearchParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { can } = useAuth()
  const { unread } = useEvents()
  const tab = sp.get('tab') ?? 'overview'

  const { data: device, error, isLoading } = useQuery({
    queryKey: qk.deviceDetail(id),
    queryFn: () => deviceApi.get(id),
    refetchInterval: 30_000,
  })

  const refresh = useMutation({
    mutationFn: () => deviceApi.runCommand(id, { kind: 'refresh', timeout_sec: 300 }),
    onSuccess: () => {
      showSuccess('已要求重新收集盤點資料')
      void qc.invalidateQueries({ queryKey: qk.deviceSub(id, 'commands') })
    },
  })
  const remove = useMutation({
    mutationFn: () => deviceApi.remove(id),
    onSuccess: () => {
      showSuccess('已刪除裝置')
      void qc.invalidateQueries({ queryKey: qk.devices })
      void navigate('/devices')
    },
  })

  if (isLoading) return <Skeleton active paragraph={{ rows: 10 }} />
  if (error || !device) {
    const notFound = error instanceof ApiError && error.status === 404
    return (
      <Result
        status={notFound ? '404' : 'error'}
        title={notFound ? '裝置不存在' : '無法載入裝置'}
        subTitle={error?.message}
        extra={<Button onClick={() => void navigate('/devices')}>回到裝置清單</Button>}
      />
    )
  }

  const hasBattery = device.battery_health !== null && device.battery_health !== undefined
  const showBattery = hasBattery || device.chassis_type === 'laptop'
  const chatUnread = unread[id]?.count ?? 0

  const items = [
    { key: 'overview', label: '概觀', children: <OverviewTab device={device} /> },
    { key: 'hardware', label: '硬體', children: <HardwareTab deviceId={id} /> },
    { key: 'software', label: '軟體', children: <SoftwareTab deviceId={id} /> },
    { key: 'health', label: '健康', children: <HealthTab deviceId={id} /> },
    ...(showBattery ? [{ key: 'battery', label: '電池', children: <BatteryTab deviceId={id} /> }] : []),
    { key: 'commands', label: '指令', children: <CommandsTab device={device} /> },
    ...(can('helpdesk')
      ? [
          {
            key: 'chat',
            label: (
              <Badge count={chatUnread} size="small" offset={[8, -2]}>
                訊息
              </Badge>
            ),
            children: <ChatTab device={device} />,
          },
          { key: 'remote', label: '遠端', children: <RemoteTab device={device} /> },
        ]
      : []),
  ]

  return (
    <>
      <PageHeader
        back={<Button type="text" icon={<ArrowLeftOutlined />} onClick={() => void navigate(-1)} aria-label="返回" />}
        title={
          <Space wrap size={8}>
            <span>{device.name}</span>
            <OnlineStatus online={device.online} hasAgent={device.has_agent} lastSeen={device.last_seen_at} />
            <HealthTag status={device.health_status} />
            <LifecycleTag state={device.lifecycle_state} />
          </Space>
        }
        subtitle={
          <Flex gap={8} wrap align="center">
            <span>{labelOf(deviceTypeLabels, device.device_type)}</span>
            {device.os_name && <span>· {device.os_name}</span>}
            {device.logged_in_user && <span>· {device.logged_in_user}</span>}
            {device.ip_addresses?.[0] && <span className="mono">· {device.ip_addresses[0]}</span>}
            {device.group_name && (
              <Tag icon={<FolderOutlined />} color="blue" style={{ marginInlineEnd: 0 }}>
                {device.group_name}
              </Tag>
            )}
            {device.tags?.map((t) => (
              <Tag key={t} style={{ marginInlineEnd: 0 }}>
                {t}
              </Tag>
            ))}
          </Flex>
        }
        extra={
          <>
            {device.has_agent && can('helpdesk') && (
              <Button icon={<ReloadOutlined />} loading={refresh.isPending} disabled={!device.online} onClick={() => refresh.mutate()}>
                重新整理盤點
              </Button>
            )}
            {can('admin') && (
              <Popconfirm
                title="刪除裝置紀錄？"
                description={device.has_agent ? 'Agent 會收到解除註冊通知，此操作無法復原。' : '此操作無法復原。'}
                okText="刪除"
                okButtonProps={{ danger: true }}
                onConfirm={() => remove.mutateAsync()}
              >
                <Button danger icon={<DeleteOutlined />}>
                  刪除
                </Button>
              </Popconfirm>
            )}
          </>
        }
      />
      {!device.has_agent && (
        <Typography.Paragraph type="secondary">
          此設備未安裝 Agent（來源：{device.sources?.join('、') || '-'}），僅能管理資產與生命週期資訊。
        </Typography.Paragraph>
      )}
      <Tabs
        activeKey={items.some((i) => i.key === tab) ? tab : 'overview'}
        onChange={(k) => setSp((p) => {
          const n = new URLSearchParams(p)
          n.set('tab', k)
          return n
        }, { replace: true })}
        items={items.map((i) => ({ ...i, children: <Suspense fallback={<FullPageSpin />}>{i.children}</Suspense> }))}
        destroyOnHidden
      />
    </>
  )
}
