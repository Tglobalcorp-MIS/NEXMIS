import { useState } from 'react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Card, Flex, Popconfirm, Space, Table, Tooltip, Typography } from 'antd'
import {
  BellOutlined,
  CodeOutlined,
  LockOutlined,
  PoweroffOutlined,
  ReloadOutlined,
  SyncOutlined,
} from '@ant-design/icons'
import { commandApi, deviceApi } from '../../../api/endpoints'
import { qk } from '../../../api/keys'
import type { Command, DeviceDetail, NewCommand } from '../../../api/types'
import { CommandStatusTag } from '../../../components/StatusTags'
import { NotifyModal, RebootModal, RunCommandDrawer } from '../../../components/CommandModals'
import { CommandDetail } from '../../../components/CommandDetail'
import { commandSummary } from '../../../utils/command'
import { useAuth } from '../../../hooks/useAuth'
import { editableKinds, helpdeskKinds } from '../../../utils/command'
import { durationBetween, fmtTime } from '../../../utils/format'
import { commandKindLabels, commandPendingStatuses, labelOf } from '../../../utils/labels'
import { showSuccess } from '../../../utils/feedback'

type Dialog = 'run' | 'reboot' | 'shutdown' | 'notify' | null

export default function CommandsTab({ device }: { device: DeviceDetail }) {
  const { can } = useAuth()
  const qc = useQueryClient()
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const [dialog, setDialog] = useState<Dialog>(null)
  const params = { page, page_size: pageSize }
  const isOperator = can('operator')
  const isHelpdesk = can('helpdesk')

  const { data, isFetching, refetch } = useQuery({
    queryKey: qk.deviceSub(device.id, 'commands', params),
    queryFn: () => deviceApi.commands(device.id, params),
    placeholderData: keepPreviousData,
  })

  const send = async (cmd: NewCommand) => {
    const c = await deviceApi.runCommand(device.id, cmd)
    showSuccess(`已派送「${labelOf(commandKindLabels, c.kind)}」`)
    void qc.invalidateQueries({ queryKey: qk.deviceSub(device.id, 'commands') })
    void qc.invalidateQueries({ queryKey: qk.commands })
  }
  const quick = useMutation({ mutationFn: send })
  const cancel = useMutation({
    mutationFn: (id: string) => commandApi.cancel(id),
    onSuccess: () => {
      showSuccess('已取消指令')
      void qc.invalidateQueries({ queryKey: qk.deviceSub(device.id, 'commands') })
    },
  })

  const offlineHint = !device.online ? '裝置目前離線，指令將排隊至上線後執行' : undefined

  return (
    <Flex vertical gap={16}>
      {device.has_agent && isHelpdesk && (
        <Card size="small" title="快速動作">
          <Space wrap>
            {isOperator && (
              <Button type="primary" icon={<CodeOutlined />} onClick={() => setDialog('run')}>
                執行指令
              </Button>
            )}
            <Tooltip title={offlineHint}>
              <Button icon={<SyncOutlined />} loading={quick.isPending && quick.variables?.kind === 'refresh'} onClick={() => quick.mutate({ kind: 'refresh', timeout_sec: 300 })}>
                重新整理盤點
              </Button>
            </Tooltip>
            {isOperator && (
              <>
                <Button icon={<ReloadOutlined />} onClick={() => setDialog('reboot')}>
                  安全重新開機
                </Button>
                <Button danger icon={<PoweroffOutlined />} onClick={() => setDialog('shutdown')}>
                  關機
                </Button>
              </>
            )}
            <Popconfirm title="立即鎖定此工作站？" okText="鎖定" onConfirm={() => quick.mutateAsync({ kind: 'lock', timeout_sec: 120 })}>
              <Button icon={<LockOutlined />}>鎖定</Button>
            </Popconfirm>
            <Button icon={<BellOutlined />} onClick={() => setDialog('notify')}>
              傳送通知
            </Button>
          </Space>
          {offlineHint && (
            <div style={{ marginTop: 8 }}>
              <Typography.Text type="warning">{offlineHint}</Typography.Text>
            </div>
          )}
        </Card>
      )}

      <Card title="指令歷史" extra={<Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching} />}>
        <Table<Command>
          rowKey="id"
          size="small"
          dataSource={data?.items}
          loading={isFetching && !data}
          scroll={{ x: 900 }}
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
            { title: '類型', dataIndex: 'kind', width: 120, render: (k: string) => labelOf(commandKindLabels, k) },
            {
              title: '內容',
              key: 'summary',
              ellipsis: true,
              render: (_, c) => <Typography.Text className="mono" style={{ fontSize: 12 }}>{commandSummary(c)}</Typography.Text>,
            },
            { title: '狀態', dataIndex: 'status', width: 90, render: (s: string) => <CommandStatusTag status={s} /> },
            { title: '結束碼', dataIndex: 'exit_code', width: 70, render: (v: number | null) => v ?? '-' },
            { title: '耗時', key: 'dur', width: 90, render: (_, c) => durationBetween(c.started_at, c.finished_at) },
            { title: '執行者', dataIndex: 'issued_by_name', width: 110 },
            {
              title: '',
              key: 'act',
              width: 70,
              render: (_, c) =>
                isOperator && commandPendingStatuses.includes(c.status) ? (
                  <Popconfirm title="取消此指令？" onConfirm={() => cancel.mutateAsync(c.id)}>
                    <Button size="small" type="link" danger>
                      取消
                    </Button>
                  </Popconfirm>
                ) : null,
            },
          ]}
        />
      </Card>

      <RunCommandDrawer
        open={dialog === 'run'}
        onClose={() => setDialog(null)}
        title={`執行指令：${device.name}`}
        onSubmit={send}
        kinds={isOperator ? editableKinds : helpdeskKinds}
      />
      <RebootModal open={dialog === 'reboot'} onClose={() => setDialog(null)} onSubmit={send} />
      <RebootModal open={dialog === 'shutdown'} kind="shutdown" onClose={() => setDialog(null)} onSubmit={send} />
      <NotifyModal open={dialog === 'notify'} onClose={() => setDialog(null)} onSubmit={send} />
    </Flex>
  )
}
