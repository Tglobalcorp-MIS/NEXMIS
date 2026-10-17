import { useState } from 'react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router'
import { Button, Card, Flex, Popconfirm, Select, Table, Typography } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { commandApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { Command, CommandKind, CommandListParams, CommandStatus } from '../../api/types'
import { PageHeader } from '../../components/PageHeader'
import { CommandStatusTag } from '../../components/StatusTags'
import { CommandDetail } from '../../components/CommandDetail'
import { useAuth } from '../../hooks/useAuth'
import { commandSummary } from '../../utils/command'
import { durationBetween, fmtTime } from '../../utils/format'
import { commandKindLabels, commandPendingStatuses, commandStatusLabels, labelOf, toOptions } from '../../utils/labels'
import { showSuccess } from '../../utils/feedback'

export default function Commands() {
  const { can } = useAuth()
  const qc = useQueryClient()
  const [status, setStatus] = useState<CommandStatus | undefined>()
  const [kind, setKind] = useState<CommandKind | undefined>()
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const params: CommandListParams = { status, kind, page, page_size: pageSize }
  const { data, isFetching, refetch } = useQuery({
    queryKey: [...qk.commands, params],
    queryFn: () => commandApi.list(params),
    placeholderData: keepPreviousData,
  })
  const cancel = useMutation({
    mutationFn: commandApi.cancel,
    onSuccess: () => {
      showSuccess('已取消指令')
      void qc.invalidateQueries({ queryKey: qk.commands })
    },
  })

  return (
    <>
      <PageHeader
        title="指令紀錄"
        subtitle="所有裝置的指令派送與執行結果"
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching}>
            重新整理
          </Button>
        }
      />
      <Card>
        <Flex gap={8} wrap style={{ marginBottom: 12 }}>
          <Select allowClear placeholder="狀態" value={status} onChange={(v?: CommandStatus) => { setStatus(v); setPage(1) }} options={toOptions(commandStatusLabels)} style={{ width: 130 }} />
          <Select allowClear placeholder="類型" value={kind} onChange={(v?: CommandKind) => { setKind(v); setPage(1) }} options={toOptions(commandKindLabels)} style={{ width: 160 }} />
        </Flex>
        <Table<Command>
          rowKey="id"
          size="small"
          dataSource={data?.items}
          loading={isFetching && !data}
          scroll={{ x: 1000 }}
          expandable={{ expandedRowRender: (c) => <CommandDetail command={c} /> }}
          pagination={{
            current: page,
            pageSize,
            total: data?.total ?? 0,
            showSizeChanger: true,
            onChange: (p, s) => {
              setPage(p)
              setPageSize(s)
            },
            showTotal: (t) => `共 ${t} 筆`,
          }}
          columns={[
            { title: '時間', dataIndex: 'created_at', width: 150, render: (v: string) => fmtTime(v, true) },
            { title: '裝置', dataIndex: 'device_name', width: 140, render: (v: string, r) => <Link to={`/devices/${r.device_id}?tab=commands`}>{v}</Link> },
            { title: '類型', dataIndex: 'kind', width: 120, render: (k: string) => labelOf(commandKindLabels, k) },
            { title: '內容', key: 'sum', ellipsis: true, render: (_, c) => <Typography.Text className="mono" style={{ fontSize: 12 }}>{commandSummary(c)}</Typography.Text> },
            { title: '狀態', dataIndex: 'status', width: 90, render: (s: string) => <CommandStatusTag status={s} /> },
            { title: '結束碼', dataIndex: 'exit_code', width: 70, render: (v: number | null) => v ?? '-' },
            { title: '耗時', key: 'dur', width: 90, render: (_, c) => durationBetween(c.started_at, c.finished_at) },
            {
              title: '來源',
              key: 'src',
              width: 90,
              render: (_, c) =>
                c.deployment_id ? <Link to={`/deployments/${c.deployment_id}`}>部署</Link> : c.schedule_id ? <Link to="/schedules">排程</Link> : '手動',
            },
            { title: '執行者', dataIndex: 'issued_by_name', width: 100 },
            {
              title: '',
              key: 'act',
              width: 70,
              render: (_, c) =>
                can('operator') && commandPendingStatuses.includes(c.status) ? (
                  <Popconfirm title="取消此指令？" onConfirm={() => cancel.mutateAsync(c.id)}>
                    <Button type="link" size="small" danger>
                      取消
                    </Button>
                  </Popconfirm>
                ) : null,
            },
          ]}
        />
      </Card>
    </>
  )
}
