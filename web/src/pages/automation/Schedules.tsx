import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Card, Col, Drawer, Flex, Form, Input, Popconfirm, Radio, Row, Space, Switch, Table, Tag, Tooltip, Typography } from 'antd'
import type { FormInstance } from 'antd'
import { DeleteOutlined, EditOutlined, PlayCircleOutlined, PlusOutlined } from '@ant-design/icons'
import { scheduleApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { Schedule, ScheduleAction, ScheduleInput, Target } from '../../api/types'
import { PageHeader } from '../../components/PageHeader'
import { TargetSelector } from '../../components/TargetSelector'
import { CommandEditor } from '../../components/CommandEditor'
import { PackageSelect } from '../../components/Selectors'
import { useAuth } from '../../hooks/useAuth'
import { useGroupName } from '../../hooks/useGroups'
import { packageApi } from '../../api/endpoints'
import { cronTemplates, describeCron, validateCron } from '../../utils/cron'
import { fromNewCommand, toNewCommand, type CommandFormValues } from '../../utils/command'
import { describeTarget, emptyTarget, targetValidator } from '../../utils/target'
import { fmtTime } from '../../utils/format'
import { commandKindLabels, labelOf } from '../../utils/labels'
import { showSuccess } from '../../utils/feedback'

export default function Schedules() {
  const { can } = useAuth()
  const qc = useQueryClient()
  const groupName = useGroupName()
  const [editing, setEditing] = useState<Schedule | 'new' | null>(null)
  const { data, isLoading } = useQuery({ queryKey: qk.schedules, queryFn: scheduleApi.list })
  const { data: packages } = useQuery({ queryKey: qk.packages, queryFn: packageApi.list })
  const invalidate = () => void qc.invalidateQueries({ queryKey: qk.schedules })
  const toggle = useMutation({ mutationFn: (s: Schedule) => scheduleApi.update(s.id, { enabled: !s.enabled }), onSuccess: invalidate })
  const run = useMutation({ mutationFn: scheduleApi.run, onSuccess: () => { showSuccess('已觸發執行'); invalidate() } })
  const remove = useMutation({ mutationFn: scheduleApi.remove, onSuccess: () => { showSuccess('已刪除排程'); invalidate() } })

  const describeAction = (a: ScheduleAction) => {
    if (a.type === 'refresh') return '重新整理盤點'
    if (a.type === 'deployment') {
      const p = packages?.find((x) => x.id === a.package_id)
      return `部署 ${p ? `${p.name} ${p.version}` : '套件'}`
    }
    return labelOf(commandKindLabels, a.command?.kind)
  }

  return (
    <>
      <PageHeader
        title="排程"
        subtitle="以 Cron 定期執行指令、部署或盤點"
        extra={
          can('operator') && (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setEditing('new')}>
              新增排程
            </Button>
          )
        }
      />
      <Card>
        <Table<Schedule>
          rowKey="id"
          dataSource={data}
          loading={isLoading}
          pagination={{ pageSize: 50, hideOnSinglePage: true }}
          scroll={{ x: 1100 }}
          columns={[
            {
              title: '啟用',
              dataIndex: 'enabled',
              width: 70,
              render: (v: boolean, r) => <Switch size="small" checked={v} disabled={!can('operator')} loading={toggle.isPending && toggle.variables?.id === r.id} onChange={() => toggle.mutate(r)} />,
            },
            { title: '名稱', dataIndex: 'name', render: (v: string) => <Typography.Text strong>{v}</Typography.Text> },
            {
              title: '排程',
              dataIndex: 'cron',
              width: 220,
              render: (v: string) => (
                <div>
                  <Tag className="mono">{v}</Tag>
                  <div>
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                      {describeCron(v)}
                    </Typography.Text>
                  </div>
                </div>
              ),
            },
            { title: '動作', dataIndex: 'action', width: 160, render: (a: ScheduleAction) => describeAction(a) },
            { title: '目標', dataIndex: 'target', width: 180, ellipsis: true, render: (t: Target) => describeTarget(t, groupName) },
            { title: '上次執行', dataIndex: 'last_run_at', width: 140, render: (v: string | null) => fmtTime(v) },
            { title: '下次執行', dataIndex: 'next_run_at', width: 140, render: (v: string | null, r) => (r.enabled ? fmtTime(v) : <Typography.Text type="secondary">已停用</Typography.Text>) },
            { title: '建立者', dataIndex: 'created_by_name', width: 100 },
            {
              title: '操作',
              key: 'act',
              width: 130,
              fixed: 'right',
              render: (_, r) =>
                can('operator') && (
                  <Space size={0}>
                    <Popconfirm title={`立即執行「${r.name}」一次？`} onConfirm={() => run.mutateAsync(r.id)}>
                      <Tooltip title="立即執行">
                        <Button type="link" size="small" icon={<PlayCircleOutlined />} aria-label="立即執行" />
                      </Tooltip>
                    </Popconfirm>
                    <Button type="link" size="small" icon={<EditOutlined />} onClick={() => setEditing(r)} aria-label="編輯" />
                    <Popconfirm title={`刪除「${r.name}」？`} okButtonProps={{ danger: true }} onConfirm={() => remove.mutateAsync(r.id)}>
                      <Button type="link" size="small" danger icon={<DeleteOutlined />} aria-label="刪除" />
                    </Popconfirm>
                  </Space>
                ),
            },
          ]}
        />
      </Card>
      <ScheduleDrawer value={editing} onClose={() => setEditing(null)} />
    </>
  )
}

interface FormValues {
  name: string
  cron: string
  enabled: boolean
  action_type: ScheduleAction['type']
  package_id?: string
  command: CommandFormValues
  target: Target
}

function CronField({ form }: { form: FormInstance<FormValues> }) {
  const cron = Form.useWatch('cron', form) ?? ''
  const err = cron ? validateCron(cron) : null
  return (
    <>
      <Form.Item
        label="Cron 運算式（分 時 日 月 週）"
        name="cron"
        rules={[{ required: true, message: '請輸入 Cron' }, { validator: (_, v: string) => (v && validateCron(v) ? Promise.reject(new Error(validateCron(v)!)) : Promise.resolve()) }]}
        extra={cron && !err ? `說明：${describeCron(cron)}（時區依伺服器設定）` : '例如 0 3 * * 1 表示每週一 03:00'}
      >
        <Input className="mono" placeholder="0 3 * * 1" />
      </Form.Item>
      <Flex gap={6} wrap style={{ marginTop: -12, marginBottom: 16 }}>
        {cronTemplates.map((t) => (
          <Tag key={t.value} className="clickable" color={cron === t.value ? 'blue' : undefined} onClick={() => form.setFieldValue('cron', t.value)}>
            {t.label}
          </Tag>
        ))}
      </Flex>
    </>
  )
}

function ScheduleDrawer({ value, onClose }: { value: Schedule | 'new' | null; onClose: () => void }) {
  const [form] = Form.useForm<FormValues>()
  const qc = useQueryClient()
  const isNew = value === 'new'
  const actionType = Form.useWatch('action_type', form)
  const m = useMutation({
    mutationFn: (b: ScheduleInput) => (isNew || !value ? scheduleApi.create(b) : scheduleApi.update(value.id, b)),
    onSuccess: () => {
      showSuccess(isNew ? '已新增排程' : '已更新排程')
      void qc.invalidateQueries({ queryKey: qk.schedules })
      onClose()
    },
  })

  const initial: FormValues =
    value && value !== 'new'
      ? {
          name: value.name,
          cron: value.cron,
          enabled: value.enabled,
          action_type: value.action.type,
          package_id: value.action.type === 'deployment' ? value.action.package_id : undefined,
          command: fromNewCommand(value.action.type === 'command' ? value.action.command : undefined),
          target: value.target ?? emptyTarget(),
        }
      : { name: '', cron: '0 3 * * *', enabled: true, action_type: 'command', command: fromNewCommand(undefined), target: emptyTarget() }

  const submit = async () => {
    const v = await form.validateFields()
    const action: ScheduleAction =
      v.action_type === 'refresh'
        ? { type: 'refresh' }
        : v.action_type === 'deployment'
          ? { type: 'deployment', package_id: v.package_id! }
          : { type: 'command', command: toNewCommand(v.command) }
    m.mutate({ name: v.name, cron: v.cron.trim(), enabled: v.enabled, action, target: v.target })
  }

  return (
    <Drawer
      title={isNew ? '新增排程' : '編輯排程'}
      open={!!value}
      onClose={onClose}
      size={760}
      destroyOnHidden
      extra={
        <Space>
          <Button onClick={onClose}>取消</Button>
          <Button type="primary" loading={m.isPending} onClick={() => void submit()}>
            儲存
          </Button>
        </Space>
      }
    >
      <Form form={form} layout="vertical" initialValues={initial} preserve={false}>
        <Row gutter={12}>
          <Col span={18}>
            <Form.Item label="名稱" name="name" rules={[{ required: true, message: '請輸入名稱' }]}>
              <Input placeholder="每週清除暫存" />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item label="啟用" name="enabled" valuePropName="checked">
              <Switch />
            </Form.Item>
          </Col>
        </Row>
        <CronField form={form} />
        <Form.Item label="目標" name="target" rules={[{ validator: targetValidator }]}>
          <TargetSelector />
        </Form.Item>
        <Form.Item label="動作類型" name="action_type">
          <Radio.Group optionType="button" buttonStyle="solid">
            <Radio.Button value="command">執行指令</Radio.Button>
            <Radio.Button value="deployment">部署軟體</Radio.Button>
            <Radio.Button value="refresh">重新整理盤點</Radio.Button>
          </Radio.Group>
        </Form.Item>
        {actionType === 'command' && <CommandEditor form={form} prefix={['command']} />}
        {actionType === 'deployment' && (
          <Form.Item label="套件" name="package_id" rules={[{ required: true, message: '請選擇套件' }]}>
            <PackageSelect />
          </Form.Item>
        )}
        {actionType === 'refresh' && <Typography.Paragraph type="secondary">定期要求目標裝置重新收集硬體、軟體、健康與電池資料。</Typography.Paragraph>}
      </Form>
    </Drawer>
  )
}
