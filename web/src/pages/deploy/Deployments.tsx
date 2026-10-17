import { useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router'
import { Button, Card, Table } from 'antd'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { deploymentApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { Deployment, Target } from '../../api/types'
import { PageHeader } from '../../components/PageHeader'
import { DeploymentProgress, NewDeploymentModal } from '../../components/Deployment'
import { DeploymentStatusTag } from '../../components/StatusTags'
import { RoleGate } from '../../components/RoleGate'
import { useGroupName } from '../../hooks/useGroups'
import { describeTarget } from '../../utils/target'
import { fmtTime } from '../../utils/format'

export default function Deployments() {
  const navigate = useNavigate()
  const groupName = useGroupName()
  const [open, setOpen] = useState(false)
  const [page, setPage] = useState(1)
  const params = { page, page_size: 50 }
  const { data, isFetching, refetch } = useQuery({
    queryKey: [...qk.deployments, params],
    queryFn: () => deploymentApi.list(params),
    placeholderData: keepPreviousData,
  })

  return (
    <>
      <PageHeader
        title="部署工作"
        subtitle="將套件靜默安裝至指定裝置、群組或標籤"
        extra={
          <>
            <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching}>
              重新整理
            </Button>
            <RoleGate min="operator">
              <Button type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>
                新增部署
              </Button>
            </RoleGate>
          </>
        }
      />
      <Card>
        <Table<Deployment>
          rowKey="id"
          dataSource={data?.items}
          loading={isFetching && !data}
          scroll={{ x: 1000 }}
          onRow={(r) => ({ onClick: () => void navigate(`/deployments/${r.id}`), style: { cursor: 'pointer' } })}
          pagination={{ current: page, pageSize: 50, total: data?.total ?? 0, onChange: setPage, hideOnSinglePage: true }}
          columns={[
            { title: '名稱', dataIndex: 'name', render: (v: string, r) => <Link to={`/deployments/${r.id}`}>{v}</Link> },
            { title: '套件', dataIndex: 'package_name', width: 180 },
            { title: '目標', dataIndex: 'target', width: 180, ellipsis: true, render: (t: Target) => describeTarget(t, groupName) },
            { title: '狀態', dataIndex: 'status', width: 90, render: (s: Deployment['status']) => <DeploymentStatusTag status={s} /> },
            { title: '進度', key: 'progress', width: 200, render: (_, r) => <DeploymentProgress d={r} /> },
            { title: '排程時間', dataIndex: 'scheduled_at', width: 140, render: (v: string | null) => (v ? fmtTime(v) : '立即') },
            { title: '建立者', dataIndex: 'created_by_name', width: 100 },
            { title: '建立時間', dataIndex: 'created_at', width: 140, render: (v: string) => fmtTime(v) },
          ]}
        />
      </Card>
      <NewDeploymentModal open={open} onClose={() => setOpen(false)} onCreated={(id) => void navigate(`/deployments/${id}`)} />
    </>
  )
}

