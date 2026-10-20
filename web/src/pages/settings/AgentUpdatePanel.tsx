import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Descriptions, Popconfirm, Row, Select, Skeleton, Space, Statistic, Table, Tag } from 'antd'
import { CloudUploadOutlined, ReloadOutlined } from '@ant-design/icons'
import { settingsApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { AgentUpdateStatus, AgentVersionState } from '../../api/types'
import { statusColors } from '../../hooks/useChartTheme'
import { fmtDuration, fmtTime } from '../../utils/format'
import { showSuccess } from '../../utils/feedback'

const stateTag: Record<AgentVersionState, { color: string; label: string }> = {
  current: { color: 'green', label: '已是最新' },
  newer: { color: 'blue', label: '較發佈版本新' },
  outdated: { color: 'orange', label: '待更新' },
  unknown: { color: 'default', label: '無法比較' },
}

type AutoMode = 'manifest' | 'on' | 'off'

function autoMode(s: AgentUpdateStatus): AutoMode {
  if (s.auto_update_source === 'manifest') return 'manifest'
  return s.auto_update ? 'on' : 'off'
}

export default function AgentUpdatePanel() {
  const qc = useQueryClient()
  const key = qk.settings('agent-update')
  const { data, isLoading, refetch, isFetching } = useQuery({ queryKey: key, queryFn: settingsApi.getAgentUpdate, refetchInterval: 30_000 })
  const setAuto = useMutation({
    mutationFn: (m: AutoMode) => settingsApi.putAgentUpdate({ auto_update: m === 'manifest' ? null : m === 'on' }),
    onSuccess: (d) => {
      showSuccess('已儲存自動更新設定')
      qc.setQueryData(key, d)
    },
  })
  const push = useMutation({
    mutationFn: settingsApi.pushAgentUpdate,
    onSuccess: (r) => {
      showSuccess(`已開始推送 ${r.version} 給 ${r.agents} 台線上 Agent，會在數分鐘內分批下載`)
      void qc.invalidateQueries({ queryKey: key })
    },
  })
  if (isLoading || !data) return <Skeleton active />
  const published = data.version !== ''
  return (
    <Card
      title="Agent 版本與自動更新"
      style={{ maxWidth: 860, marginBottom: 16 }}
      extra={<Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching} />}
    >
      {!published && (
        <Alert type="warning" showIcon title="伺服器尚未發佈 Agent 版本（找不到 agent/manifest.json）。" style={{ marginBottom: 16 }} />
      )}
      <Descriptions size="small" column={{ xs: 1, sm: 2 }} style={{ marginBottom: 16 }}>
        <Descriptions.Item label="發佈版本">{published ? <Tag color="blue">{data.version}</Tag> : '—'}</Descriptions.Item>
        <Descriptions.Item label="平台">{data.platforms.join(', ') || '—'}</Descriptions.Item>
        <Descriptions.Item label="自動更新">
          <Select<AutoMode>
            size="small"
            style={{ width: 220 }}
            value={autoMode(data)}
            loading={setAuto.isPending}
            onChange={(v) => setAuto.mutate(v)}
            options={[
              { value: 'manifest', label: `沿用 manifest（${data.manifest_auto_update ? '開啟' : '關閉'}）` },
              { value: 'on', label: '開啟' },
              { value: 'off', label: '關閉' },
            ]}
          />
        </Descriptions.Item>
        <Descriptions.Item label="Agent 定期檢查">約每 {fmtDuration(data.update_check_sec)}</Descriptions.Item>
        <Descriptions.Item label="上次推送">
          {data.last_push_at ? `${fmtTime(data.last_push_at)}（${data.last_push_agents} 台）` : '—'}
        </Descriptions.Item>
        <Descriptions.Item label="線上連線">{data.connected_agents} 台</Descriptions.Item>
      </Descriptions>
      <Row gutter={16} style={{ marginBottom: 16 }}>
        <Col xs={12} sm={6}>
          <Statistic title="已是最新" value={data.summary.current} styles={{ content: { color: statusColors.good } }} />
        </Col>
        <Col xs={12} sm={6}>
          <Statistic title="待更新（線上）" value={data.summary.outdated} styles={{ content: { color: data.summary.outdated ? statusColors.serious : undefined } }} />
        </Col>
        <Col xs={12} sm={6}>
          <Statistic title="離線" value={data.summary.offline} />
        </Col>
        <Col xs={12} sm={6}>
          <Statistic title="無法比較" value={data.summary.unknown} />
        </Col>
      </Row>
      <Table
        size="small"
        rowKey="version"
        pagination={false}
        dataSource={data.versions}
        style={{ marginBottom: 16 }}
        columns={[
          { title: '版本', dataIndex: 'version', render: (v: string) => v || '（未回報）' },
          { title: '狀態', dataIndex: 'state', render: (s: AgentVersionState) => <Tag color={stateTag[s].color}>{stateTag[s].label}</Tag> },
          { title: '線上', dataIndex: 'online', align: 'right' },
          { title: '離線', dataIndex: 'offline', align: 'right' },
        ]}
      />
      <Space wrap>
        <Popconfirm
          title="立即推送更新"
          description={`會把 ${data.version} 推送給所有線上 Agent，分批在數分鐘內下載並重新啟動 Agent 服務。`}
          okText="推送"
          cancelText="取消"
          onConfirm={() => push.mutate()}
          disabled={!published || !data.auto_update}
        >
          <Button type="primary" icon={<CloudUploadOutlined />} loading={push.isPending} disabled={!published || !data.auto_update}>
            立即推送更新
          </Button>
        </Popconfirm>
        {published && !data.auto_update && <span style={{ color: statusColors.unknown }}>自動更新關閉時，可改用裝置指令「更新 Agent」分批更新。</span>}
      </Space>
    </Card>
  )
}
