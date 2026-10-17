import { useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Button, Card, DatePicker, Flex, Input, Table, Tag, Tooltip, Typography } from 'antd'
import { DownloadOutlined, ReloadOutlined } from '@ant-design/icons'
import type { Dayjs } from 'dayjs'
import { Link } from 'react-router'
import { auditApi } from '../api/endpoints'
import { API_BASE, buildQuery } from '../api/client'
import { qk } from '../api/keys'
import type { AuditEntry, AuditParams } from '../api/types'
import { PageHeader } from '../components/PageHeader'
import { JsonView } from '../components/JsonView'
import { useDebounced } from '../hooks/useDebounced'
import { fmtTime } from '../utils/format'

const actionPrefixLabels: Record<string, string> = {
  auth: '驗證',
  device: '裝置',
  command: '指令',
  remote: '遠端',
  chat: '訊息',
  deployment: '部署',
  package: '套件',
  script: '指令庫',
  schedule: '排程',
  settings: '設定',
  user: '使用者',
  group: '群組',
  discovery: '探索',
  enrollment: '註冊權杖',
}

export default function Audit() {
  const [actor, setActor] = useState('')
  const [action, setAction] = useState('')
  const [q, setQ] = useState('')
  const [range, setRange] = useState<[Dayjs | null, Dayjs | null] | null>(null)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const dActor = useDebounced(actor, 400)
  const dAction = useDebounced(action, 400)
  const dQ = useDebounced(q, 400)

  const filters: AuditParams = {
    actor: dActor || undefined,
    action: dAction || undefined,
    q: dQ || undefined,
    from: range?.[0]?.startOf('day').toISOString(),
    to: range?.[1]?.endOf('day').toISOString(),
  }
  const params = { ...filters, page, page_size: pageSize }
  const { data, isFetching, refetch } = useQuery({
    queryKey: [...qk.audit, params],
    queryFn: () => auditApi.list(params),
    placeholderData: keepPreviousData,
  })

  const exportUrl = `${API_BASE}/audit/export${buildQuery({ format: 'csv', ...filters })}`
  const resetPage = <T,>(fn: (v: T) => void) => (v: T) => {
    fn(v)
    setPage(1)
  }

  return (
    <>
      <PageHeader
        title="稽核紀錄"
        subtitle="所有寫入操作、登入、遠端連線與指令內容（僅可新增、不可修改）"
        extra={
          <>
            <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching}>
              重新整理
            </Button>
            <Button type="primary" icon={<DownloadOutlined />} href={exportUrl} download>
              匯出 CSV
            </Button>
          </>
        }
      />
      <Card>
        <Flex gap={8} wrap style={{ marginBottom: 12 }}>
          <Input allowClear placeholder="操作者" value={actor} onChange={(e) => resetPage(setActor)(e.target.value)} style={{ width: 150 }} />
          <Input allowClear placeholder="動作（如 command.create）" value={action} onChange={(e) => resetPage(setAction)(e.target.value)} style={{ width: 220 }} />
          <DatePicker.RangePicker value={range} onChange={(v) => resetPage(setRange)(v as [Dayjs | null, Dayjs | null] | null)} />
          <Input.Search allowClear placeholder="關鍵字（目標名稱、詳細內容）" value={q} onChange={(e) => resetPage(setQ)(e.target.value)} style={{ width: 260 }} />
        </Flex>
        <Table<AuditEntry>
          rowKey="id"
          size="small"
          dataSource={data?.items}
          loading={isFetching && !data}
          scroll={{ x: 1000 }}
          expandable={{
            expandedRowRender: (r) => <JsonView value={r.details ?? {}} />,
            rowExpandable: (r) => r.details !== null && r.details !== undefined,
          }}
          pagination={{
            current: page,
            pageSize,
            total: data?.total ?? 0,
            showSizeChanger: true,
            pageSizeOptions: [50, 100, 200, 500],
            onChange: (p, s) => {
              setPage(p)
              setPageSize(s)
            },
            showTotal: (t) => `共 ${t} 筆`,
          }}
          columns={[
            { title: '時間', dataIndex: 'at', width: 160, render: (v: string) => fmtTime(v, true) },
            { title: '操作者', dataIndex: 'actor_name', width: 120, render: (v: string) => v || <Typography.Text type="secondary">系統</Typography.Text> },
            { title: '來源 IP', dataIndex: 'actor_ip', width: 120, render: (v: string) => <span className="mono">{v || '-'}</span> },
            {
              title: '動作',
              dataIndex: 'action',
              width: 200,
              render: (v: string) => {
                const prefix = v.split('.')[0]
                return (
                  <span>
                    {actionPrefixLabels[prefix] && <Tag>{actionPrefixLabels[prefix]}</Tag>}
                    <span className="mono" style={{ fontSize: 12 }}>
                      {v}
                    </span>
                  </span>
                )
              },
            },
            {
              title: '目標',
              key: 'target',
              ellipsis: true,
              render: (_, r) => {
                const label = r.target_name || r.target_id || '-'
                const node = r.target_type === 'device' && r.target_id ? <Link to={`/devices/${r.target_id}`}>{label}</Link> : label
                return (
                  <Tooltip title={r.target_id ? `${r.target_type}：${r.target_id}` : undefined}>
                    {r.target_type && <Typography.Text type="secondary">{r.target_type} · </Typography.Text>}
                    {node}
                  </Tooltip>
                )
              },
            },
            {
              title: '結果',
              dataIndex: 'result',
              width: 80,
              render: (v: string) => <Tag color={v === 'success' ? 'success' : 'error'}>{v === 'success' ? '成功' : '失敗'}</Tag>,
            },
          ]}
        />
      </Card>
    </>
  )
}
