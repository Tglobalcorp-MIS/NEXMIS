import { useQuery } from '@tanstack/react-query'
import { Button, Card, Col, Descriptions, Row, Skeleton, Statistic, Tag } from 'antd'
import { DownloadOutlined } from '@ant-design/icons'
import { settingsApi } from '../../api/endpoints'
import { API_BASE } from '../../api/client'
import { qk } from '../../api/keys'
import { fmtDuration, fmtTime } from '../../utils/format'

export default function SystemInfo() {
  const { data, isLoading } = useQuery({ queryKey: qk.systemInfo, queryFn: settingsApi.systemInfo, refetchInterval: 30_000 })
  if (isLoading || !data) return <Skeleton active />
  return (
    <Row gutter={[16, 16]} style={{ maxWidth: 960 }}>
      <Col xs={12} md={8}>
        <Card>
          <Statistic title="已連線 Agent" value={data.agents_connected} />
        </Card>
      </Col>
      <Col xs={12} md={8}>
        <Card>
          <Statistic title="服務執行時間" value={fmtDuration(data.uptime_sec)} />
        </Card>
      </Col>
      <Col xs={24} md={8}>
        <Card>
          <Statistic title="伺服器版本" value={data.version || '-'} />
        </Card>
      </Col>
      <Col span={24}>
        <Card title="版本資訊">
          <Descriptions column={{ xs: 1, sm: 2, md: 2, lg: 2, xl: 2, xxl: 2 }} bordered size="small">
            <Descriptions.Item label="NEXMIS 版本">{data.version}</Descriptions.Item>
            <Descriptions.Item label="建置時間">{fmtTime(data.build_time) !== '-' ? fmtTime(data.build_time, true) : data.build_time || '-'}</Descriptions.Item>
            <Descriptions.Item label="Go 版本">{data.go_version}</Descriptions.Item>
            <Descriptions.Item label="資料庫">{data.db_version}</Descriptions.Item>
            <Descriptions.Item label="Web 管理端">{import.meta.env.MODE === 'production' ? '內嵌版本' : '開發模式'}</Descriptions.Item>
            <Descriptions.Item label="Agent 通訊協定">{data.protocol ?? '-'}</Descriptions.Item>
            <Descriptions.Item label="最新 Agent 版本">{data.agent_latest_version || '-'}</Descriptions.Item>
          </Descriptions>
        </Card>
      </Col>
      <Col span={24}>
        <Card title="Agent 安裝檔">
          {data.agent_installer_available ? (
            <Button type="primary" icon={<DownloadOutlined />} href={`${API_BASE}/agent/download`} download>
              下載 Agent 安裝檔{data.agent_latest_version ? `（${data.agent_latest_version}）` : ''}
            </Button>
          ) : (
            <Tag>伺服器尚未提供 Agent 安裝檔</Tag>
          )}
        </Card>
      </Col>
    </Row>
  )
}
