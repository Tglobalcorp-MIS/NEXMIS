import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router'
import { Button, Card, Col, Descriptions, Popconfirm, Progress, Result, Row, Segmented, Skeleton, Statistic, Table } from 'antd'
import { ArrowLeftOutlined, RedoOutlined, StopOutlined } from '@ant-design/icons'
import { deploymentApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { DeploymentResult } from '../../api/types'
import { PageHeader } from '../../components/PageHeader'
import { CommandStatusTag, DeploymentStatusTag } from '../../components/StatusTags'
import { useAuth } from '../../hooks/useAuth'
import { useGroupName } from '../../hooks/useGroups'
import { statusColors } from '../../hooks/useChartTheme'
import { describeTarget } from '../../utils/target'
import { fmtTime } from '../../utils/format'
import { showSuccess } from '../../utils/feedback'

const failedStatuses = ['failed', 'timeout', 'rejected', 'expired', 'canceled']
const doneStatuses = ['succeeded', ...failedStatuses]

export default function DeploymentDetail() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { can } = useAuth()
  const groupName = useGroupName()
  const [filter, setFilter] = useState<'all' | 'failed' | 'pending' | 'succeeded'>('all')
  const { data: d, isLoading, error } = useQuery({
    queryKey: qk.deployment(id),
    queryFn: () => deploymentApi.get(id),
    refetchInterval: (q) => (q.state.data?.status === 'running' ? 5000 : false),
  })
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: qk.deployment(id) })
    void qc.invalidateQueries({ queryKey: qk.deployments })
  }
  const cancel = useMutation({ mutationFn: () => deploymentApi.cancel(id), onSuccess: () => { showSuccess('已取消部署'); invalidate() } })
  const retry = useMutation({ mutationFn: () => deploymentApi.retry(id), onSuccess: () => { showSuccess('已重試失敗的目標'); invalidate() } })

  if (isLoading) return <Skeleton active />
  if (error || !d) return <Result status="error" title="無法載入部署" subTitle={error?.message} />

  const results = (d.results ?? []).filter((r) =>
    filter === 'all' ? true : filter === 'failed' ? failedStatuses.includes(r.status) : filter === 'succeeded' ? r.status === 'succeeded' : !doneStatuses.includes(r.status),
  )
  const done = d.succeeded + d.failed
  const active = d.status === 'running' || d.status === 'scheduled'

  return (
    <>
      <PageHeader
        back={<Button type="text" icon={<ArrowLeftOutlined />} onClick={() => void navigate('/deployments')} aria-label="返回" />}
        title={d.name}
        subtitle={d.package_name}
        extra={
          can('operator') && (
            <>
              {d.failed > 0 && (
                <Popconfirm title={`重試 ${d.failed} 台失敗的裝置？`} onConfirm={() => retry.mutateAsync()}>
                  <Button icon={<RedoOutlined />} loading={retry.isPending}>
                    重試失敗
                  </Button>
                </Popconfirm>
              )}
              {active && (
                <Popconfirm title="取消此部署？尚未執行的目標將不會安裝。" okButtonProps={{ danger: true }} onConfirm={() => cancel.mutateAsync()}>
                  <Button danger icon={<StopOutlined />} loading={cancel.isPending}>
                    取消部署
                  </Button>
                </Popconfirm>
              )}
            </>
          )
        }
      />
      <Row gutter={[16, 16]} style={{ marginBottom: 16 }}>
        <Col xs={24} lg={10}>
          <Card style={{ height: '100%' }}>
            <Progress
              type="circle"
              percent={d.total ? Math.round((done / d.total) * 100) : 0}
              success={{ percent: d.total ? Math.round((d.succeeded / d.total) * 100) : 0, strokeColor: statusColors.good }}
              strokeColor={d.failed ? statusColors.critical : undefined}
              size={120}
              style={{ float: 'left', marginRight: 24 }}
            />
            <Row gutter={8}>
              <Col span={12}>
                <Statistic title="成功" value={d.succeeded} styles={{ content: { color: statusColors.good } }} />
              </Col>
              <Col span={12}>
                <Statistic title="失敗" value={d.failed} styles={{ content: { color: d.failed ? statusColors.critical : undefined } }} />
              </Col>
              <Col span={12}>
                <Statistic title="等待中" value={d.pending} />
              </Col>
              <Col span={12}>
                <Statistic title="總數" value={d.total} />
              </Col>
            </Row>
          </Card>
        </Col>
        <Col xs={24} lg={14}>
          <Card style={{ height: '100%' }}>
            <Descriptions size="small" column={{ xs: 1, sm: 2, md: 2, lg: 2, xl: 2, xxl: 2 }}>
              <Descriptions.Item label="狀態">
                <DeploymentStatusTag status={d.status} />
              </Descriptions.Item>
              <Descriptions.Item label="套件">{d.package_name}</Descriptions.Item>
              <Descriptions.Item label="目標" span="filled">
                {describeTarget(d.target, groupName)}
              </Descriptions.Item>
              <Descriptions.Item label="排程時間">{d.scheduled_at ? fmtTime(d.scheduled_at) : '立即'}</Descriptions.Item>
              <Descriptions.Item label="建立者">{d.created_by_name}</Descriptions.Item>
              <Descriptions.Item label="建立時間">{fmtTime(d.created_at, true)}</Descriptions.Item>
            </Descriptions>
          </Card>
        </Col>
      </Row>
      <Card
        title="各裝置結果"
        extra={
          <Segmented
            value={filter}
            onChange={(v) => setFilter(v as typeof filter)}
            options={[
              { value: 'all', label: `全部 ${d.results?.length ?? 0}` },
              { value: 'failed', label: '失敗' },
              { value: 'pending', label: '等待中' },
              { value: 'succeeded', label: '成功' },
            ]}
          />
        }
      >
        <Table<DeploymentResult>
          rowKey="device_id"
          size="small"
          dataSource={results}
          pagination={{ pageSize: 100, hideOnSinglePage: true }}
          columns={[
            { title: '裝置', dataIndex: 'device_name', render: (v: string, r) => <Link to={`/devices/${r.device_id}?tab=commands`}>{v}</Link> },
            { title: '狀態', dataIndex: 'status', width: 100, render: (s: string) => <CommandStatusTag status={s} /> },
            { title: '結束碼', dataIndex: 'exit_code', width: 90, render: (v: number | null) => v ?? '-' },
            { title: '完成時間', dataIndex: 'finished_at', width: 160, render: (v: string | null) => fmtTime(v, true) },
          ]}
        />
      </Card>
    </>
  )
}
