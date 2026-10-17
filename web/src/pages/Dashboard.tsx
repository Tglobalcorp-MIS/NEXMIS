import { useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router'
import { Card, Col, Empty, Flex, Progress, Row, Skeleton, Statistic, Table, Tag, Typography } from 'antd'
import {
  CheckCircleOutlined,
  CloseCircleOutlined,
  DesktopOutlined,
  DisconnectOutlined,
  ExclamationCircleOutlined,
  MessageOutlined,
  QuestionCircleOutlined,
  WarningOutlined,
} from '@ant-design/icons'
import { dashboardApi } from '../api/endpoints'
import { qk } from '../api/keys'
import type { Command, HealthStatus } from '../api/types'
import { DonutChart, HBarChart } from '../components/charts'
import { statusColors } from '../hooks/useChartTheme'
import { CommandStatusTag, SeverityTag } from '../components/StatusTags'
import { PageHeader } from '../components/PageHeader'
import { SimpleList } from '../components/SimpleList'
import { daysUntil, fmtDate, fmtRelative } from '../utils/format'
import { commandKindLabels, deviceTypeLabels, issueLabel, labelOf, lifecycleLabels, lifecycleOrder } from '../utils/labels'

const healthMeta: { key: HealthStatus; label: string; color: string; icon: React.ReactNode }[] = [
  { key: 'ok', label: '正常', color: statusColors.good, icon: <CheckCircleOutlined /> },
  { key: 'warning', label: '警告', color: statusColors.warning, icon: <WarningOutlined /> },
  { key: 'critical', label: '嚴重', color: statusColors.critical, icon: <CloseCircleOutlined /> },
  { key: 'unknown', label: '未知', color: statusColors.unknown, icon: <QuestionCircleOutlined /> },
]

export default function Dashboard() {
  const navigate = useNavigate()
  const { data, isLoading } = useQuery({ queryKey: qk.dashboard, queryFn: dashboardApi.get, refetchInterval: 60_000 })

  if (isLoading || !data) {
    return (
      <>
        <PageHeader title="儀表板" />
        <Skeleton active paragraph={{ rows: 12 }} />
      </>
    )
  }

  const d = data.devices
  const onlinePct = d.with_agent ? Math.round((d.online / d.with_agent) * 100) : 0
  const lifecycle = [...data.by_lifecycle].sort(
    (a, b) => lifecycleOrder.indexOf(a.key as never) - lifecycleOrder.indexOf(b.key as never),
  )

  const stat = (title: string, value: number, icon: React.ReactNode, color: string, to: string, extra?: React.ReactNode) => (
    <Col xs={12} md={6}>
      <Card hoverable onClick={() => void navigate(to)} styles={{ body: { padding: 20 } }}>
        <Statistic title={title} value={value} prefix={<span style={{ color, marginRight: 4 }}>{icon}</span>} />
        {extra}
      </Card>
    </Col>
  )

  return (
    <>
      <PageHeader
        title="儀表板"
        subtitle="全公司設備即時概況"
        extra={
          data.unread_chats > 0 && (
            <Tag icon={<MessageOutlined />} color="blue" style={{ padding: '4px 10px' }}>
              {data.unread_chats} 則使用者訊息未讀
            </Tag>
          )
        }
      />
      <Row gutter={[16, 16]}>
        {stat('裝置總數', d.total, <DesktopOutlined />, '#1677ff', '/devices', (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            已裝 Agent {d.with_agent} 台
          </Typography.Text>
        ))}
        {stat('線上', d.online, <CheckCircleOutlined />, statusColors.good, '/devices?status=online', (
          <Progress percent={onlinePct} size="small" showInfo={false} strokeColor={statusColors.good} />
        ))}
        {stat('離線', d.offline, <DisconnectOutlined />, statusColors.critical, '/devices?status=offline', (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            佔已裝 Agent 裝置 {d.with_agent ? Math.round((d.offline / d.with_agent) * 100) : 0}%
          </Typography.Text>
        ))}
        {stat('未裝 Agent', d.without_agent, <ExclamationCircleOutlined />, statusColors.warning, '/devices?has_agent=false', (
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            來自 AD 同步或網段探索
          </Typography.Text>
        ))}

        <Col xs={24} lg={8}>
          <Card title="健康狀態分佈" style={{ height: '100%' }}>
            <DonutChart
              data={healthMeta.map((h) => ({ name: h.label, value: data.health[h.key] ?? 0, color: h.color }))}
              onClick={(name) => {
                const h = healthMeta.find((x) => x.label === name)
                if (h) void navigate(`/devices?health=${h.key}`)
              }}
            />
            <Flex gap={12} wrap justify="center">
              {healthMeta.map((h) => (
                <Link key={h.key} to={`/devices?health=${h.key}`}>
                  <Tag icon={h.icon} color={h.key === 'ok' ? 'success' : h.key === 'warning' ? 'warning' : h.key === 'critical' ? 'error' : 'default'}>
                    {h.label} {data.health[h.key] ?? 0}
                  </Tag>
                </Link>
              ))}
            </Flex>
          </Card>
        </Col>
        <Col xs={24} lg={8}>
          <Card title="作業系統版本" style={{ height: '100%' }}>
            {data.by_os_edition.length ? (
              <HBarChart data={data.by_os_edition.slice(0, 8).map((x) => ({ name: x.key || '未知', value: x.count }))} />
            ) : (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} />
            )}
          </Card>
        </Col>
        <Col xs={24} lg={8}>
          <Card title="生命週期分佈" style={{ height: '100%' }}>
            {lifecycle.length ? (
              <HBarChart
                data={lifecycle.map((x) => ({ name: labelOf(lifecycleLabels, x.key), value: x.count }))}
                onClick={(name) => {
                  const k = lifecycle.find((x) => labelOf(lifecycleLabels, x.key) === name)
                  if (k) void navigate(`/devices?lifecycle=${k.key}`)
                }}
              />
            ) : (
              <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} />
            )}
            {data.by_type.length > 0 && (
              <Flex gap={6} wrap style={{ marginTop: 12 }}>
                {data.by_type.map((t) => (
                  <Link key={t.key} to={`/devices?type=${t.key}`}>
                    <Tag>
                      {labelOf(deviceTypeLabels, t.key)} {t.count}
                    </Tag>
                  </Link>
                ))}
              </Flex>
            )}
          </Card>
        </Col>

        <Col xs={24} md={12} xl={8}>
          <Card title="電池健康警示" extra={<Typography.Text type="secondary">{data.battery_alerts.length} 台</Typography.Text>} style={{ height: '100%' }}>
            <SimpleList
              items={[...data.battery_alerts].sort((a, b) => a.health_percent - b.health_percent)}
              rowKey={(b) => b.device_id}
              empty="沒有電池健康度偏低的裝置"
              maxHeight={300}
              render={(b) => (
                  <Flex justify="space-between" align="center" style={{ width: '100%' }} gap={12}>
                    <Link to={`/devices/${b.device_id}?tab=battery`}>{b.device_name}</Link>
                    <Progress
                      percent={Math.round(b.health_percent)}
                      size="small"
                      style={{ width: 140, margin: 0 }}
                      strokeColor={b.health_percent < 50 ? statusColors.critical : statusColors.warning}
                    />
                  </Flex>
              )}
            />
          </Card>
        </Col>
        <Col xs={24} md={12} xl={8}>
          <Card title="保固即將到期" extra={<Typography.Text type="secondary">{data.warranty_expiring.length} 台</Typography.Text>} style={{ height: '100%' }}>
            <SimpleList
              items={data.warranty_expiring}
              rowKey={(w) => w.device_id}
              empty="近期沒有保固到期的裝置"
              maxHeight={300}
              render={(w) => {
                const days = daysUntil(w.warranty_end)
                return (
                    <Flex justify="space-between" style={{ width: '100%' }}>
                      <Link to={`/devices/${w.device_id}`}>{w.device_name}</Link>
                      <span>
                        {fmtDate(w.warranty_end)}{' '}
                        <Tag color={days !== null && days < 0 ? 'red' : days !== null && days <= 30 ? 'orange' : 'gold'}>
                          {days === null ? '-' : days < 0 ? `已過期 ${-days} 天` : `剩 ${days} 天`}
                        </Tag>
                      </span>
                    </Flex>
                )
              }}
            />
          </Card>
        </Col>
        <Col xs={24} xl={8}>
          <Card title="常見問題 Top" style={{ height: '100%' }}>
            <SimpleList
              items={data.top_issues}
              rowKey={(i) => i.code}
              empty="目前沒有偵測到問題"
              render={(i, idx) => (
                  <Flex justify="space-between" align="center" style={{ width: '100%' }} gap={8}>
                    <span>
                      <Typography.Text type="secondary" style={{ marginRight: 8 }}>
                        {idx + 1}.
                      </Typography.Text>
                      {issueLabel(i.code)}
                    </span>
                    <span>
                      <SeverityTag severity={i.severity} />
                      <Typography.Text strong>{i.count}</Typography.Text> 台
                    </span>
                  </Flex>
              )}
            />
          </Card>
        </Col>

        <Col span={24}>
          <Card title="最近指令" extra={<Link to="/commands">全部指令紀錄</Link>}>
            <Table<Command>
              rowKey="id"
              size="small"
              pagination={false}
              dataSource={data.recent_commands}
              scroll={{ x: 720 }}
              columns={[
                { title: '裝置', dataIndex: 'device_name', render: (v: string, r) => <Link to={`/devices/${r.device_id}?tab=commands`}>{v}</Link> },
                { title: '類型', dataIndex: 'kind', render: (k: string) => labelOf(commandKindLabels, k) },
                { title: '狀態', dataIndex: 'status', render: (s: string) => <CommandStatusTag status={s} /> },
                { title: '結束碼', dataIndex: 'exit_code', render: (v: number | null) => v ?? '-' },
                { title: '執行者', dataIndex: 'issued_by_name' },
                { title: '時間', dataIndex: 'created_at', render: (v: string) => fmtRelative(v) },
              ]}
            />
          </Card>
        </Col>
      </Row>
    </>
  )
}
