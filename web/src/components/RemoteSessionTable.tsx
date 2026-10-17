import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Button, Popconfirm, Space, Table, Tag, Tooltip } from 'antd'
import type { TablePaginationConfig } from 'antd'
import { Link } from 'react-router'
import { remoteApi } from '../api/endpoints'
import { qk } from '../api/keys'
import type { RemoteSession } from '../api/types'
import { durationBetween, fmtTime } from '../utils/format'
import { remoteEndReasonLabel, remoteModeLabels } from '../utils/labels'
import { showSuccess } from '../utils/feedback'
import { RemoteStatusTag } from './StatusTags'

export function RemoteSessionTable({
  data,
  loading,
  showDevice = true,
  pagination,
}: {
  data: RemoteSession[] | undefined
  loading?: boolean
  showDevice?: boolean
  pagination?: TablePaginationConfig | false
}) {
  const qc = useQueryClient()
  const stop = useMutation({
    mutationFn: remoteApi.stop,
    onSuccess: () => {
      showSuccess('已結束連線')
      void qc.invalidateQueries({ queryKey: qk.remoteSessions })
    },
  })
  return (
    <Table<RemoteSession>
      rowKey="id"
      size="small"
      dataSource={data}
      loading={loading}
      pagination={pagination}
      scroll={{ x: 1100 }}
      columns={[
        { title: '請求時間', dataIndex: 'created_at', width: 150, render: (v: string) => fmtTime(v, true) },
        ...(showDevice
          ? [
              {
                title: '裝置',
                dataIndex: 'device_name',
                width: 150,
                render: (v: string | undefined, r: RemoteSession) => <Link to={`/devices/${r.device_id}?tab=remote`}>{v || r.device_id.slice(0, 8)}</Link>,
              },
            ]
          : []),
        {
          title: '模式',
          dataIndex: 'mode',
          width: 90,
          render: (m: RemoteSession['mode']) => <Tag color={m === 'control' ? 'volcano' : 'geekblue'}>{remoteModeLabels[m] ?? m}</Tag>,
        },
        { title: '狀態', dataIndex: 'status', width: 90, render: (s: RemoteSession['status']) => <RemoteStatusTag status={s} /> },
        { title: '請求者', dataIndex: 'requester_name', width: 110 },
        { title: '原因', dataIndex: 'reason', ellipsis: true },
        { title: '同意者', dataIndex: 'consent_user', width: 130, render: (v: string) => v || '-' },
        { title: '開始', dataIndex: 'started_at', width: 150, render: (v: string | null) => fmtTime(v, true) },
        { title: '時長', key: 'dur', width: 90, render: (_: unknown, r: RemoteSession) => durationBetween(r.started_at, r.ended_at) },
        {
          title: '結束原因',
          dataIndex: 'end_reason',
          width: 120,
          ellipsis: true,
          render: (v: string) => (v ? <Tooltip title={v}>{remoteEndReasonLabel(v)}</Tooltip> : '-'),
        },
        {
          title: '操作',
          key: 'act',
          width: 140,
          fixed: 'right',
          render: (_: unknown, r: RemoteSession) =>
            r.status === 'pending' || r.status === 'active' ? (
              <Space size={0}>
                <Button type="link" size="small" onClick={() => window.open(`/remote/${r.id}`, '_blank')}>
                  開啟
                </Button>
                <Popconfirm title="結束此連線？" onConfirm={() => stop.mutateAsync(r.id)}>
                  <Button type="link" size="small" danger>
                    結束
                  </Button>
                </Popconfirm>
              </Space>
            ) : null,
        },
      ]}
    />
  )
}
