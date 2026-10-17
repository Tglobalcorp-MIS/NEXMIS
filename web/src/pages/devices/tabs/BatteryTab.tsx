import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Card, Col, Descriptions, Empty, Flex, Progress, Row, Segmented, Skeleton, Statistic, Tag, Typography } from 'antd'
import { deviceApi } from '../../../api/endpoints'
import { qk } from '../../../api/keys'
import { TimeLineChart } from '../../../components/charts'
import { statusColors } from '../../../hooks/useChartTheme'
import { fmtTime } from '../../../utils/format'

export default function BatteryTab({ deviceId }: { deviceId: string }) {
  const [days, setDays] = useState(180)
  const { data, isLoading } = useQuery({
    queryKey: qk.deviceSub(deviceId, 'battery', days),
    queryFn: () => deviceApi.battery(deviceId, days),
  })
  if (isLoading) return <Skeleton active paragraph={{ rows: 8 }} />
  const b = data?.latest
  if (!b || !b.present) return <Empty description="此裝置未偵測到電池" />
  const health = b.health_percent ?? 0
  const color = health < 50 ? statusColors.critical : health < 70 ? statusColors.warning : statusColors.good
  const history = (data?.history ?? []).filter((p) => p.health_percent !== null)

  return (
    <Row gutter={[16, 16]}>
      <Col xs={24} lg={8}>
        <Card title="電池健康度" style={{ height: '100%' }}>
          <Flex vertical align="center" gap={8}>
            <Progress type="dashboard" percent={Math.round(health)} size={180} strokeColor={color} format={(p) => `${p}%`} />
            <Tag color={health < 50 ? 'error' : health < 70 ? 'warning' : 'success'}>
              {health < 50 ? '建議更換' : health < 70 ? '老化中' : '良好'}
            </Tag>
            <Typography.Text type="secondary">
              目前電量 {b.charge_percent ?? '-'}%，{b.on_ac ? '使用 AC 電源' : '使用電池'}
              {!b.on_ac && b.estimated_runtime_min ? `，約可使用 ${b.estimated_runtime_min} 分鐘` : ''}
            </Typography.Text>
          </Flex>
        </Card>
      </Col>
      <Col xs={24} lg={16}>
        <Card title="容量資訊" style={{ height: '100%' }}>
          <Row gutter={16}>
            <Col span={8}>
              <Statistic title="設計容量" value={b.design_capacity_mwh ?? 0} suffix="mWh" />
            </Col>
            <Col span={8}>
              <Statistic title="充飽容量" value={b.full_charge_capacity_mwh ?? 0} suffix="mWh" />
            </Col>
            <Col span={8}>
              <Statistic title="循環次數" value={b.cycle_count ?? 0} suffix="次" />
            </Col>
          </Row>
          <Descriptions size="small" column={{ xs: 1, sm: 2, md: 2, lg: 2, xl: 2, xxl: 2 }} style={{ marginTop: 24 }} bordered>
            <Descriptions.Item label="名稱">{b.name || '-'}</Descriptions.Item>
            <Descriptions.Item label="製造商">{b.manufacturer || '-'}</Descriptions.Item>
            <Descriptions.Item label="序號">{b.serial_number || '-'}</Descriptions.Item>
            <Descriptions.Item label="化學類型">{b.chemistry || '-'}</Descriptions.Item>
            <Descriptions.Item label="收集時間" span="filled">
              {fmtTime(b.collected)}
            </Descriptions.Item>
          </Descriptions>
        </Card>
      </Col>
      <Col span={24}>
        <Card
          title="健康度歷史"
          extra={
            <Segmented
              value={days}
              onChange={(v) => setDays(v)}
              options={[
                { label: '30 天', value: 30 },
                { label: '180 天', value: 180 },
                { label: '1 年', value: 365 },
              ]}
            />
          }
        >
          {history.length ? (
            <TimeLineChart
              data={history}
              xKey="collected_at"
              series={[{ key: 'health_percent', name: '健康度' }]}
              yDomain={[0, 100]}
              timeFormat="YYYY-MM-DD"
            />
          ) : (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="此期間沒有資料" />
          )}
        </Card>
      </Col>
    </Row>
  )
}
