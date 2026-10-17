import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Card, Col, Drawer, Form, Input, InputNumber, Popconfirm, Row, Select, Space, Table, Tag, Typography } from 'antd'
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons'
import { scriptApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { Script, ScriptInput } from '../../api/types'
import { PageHeader } from '../../components/PageHeader'
import { CodeArea } from '../../components/CodeArea'
import { OutputBlock } from '../../components/JsonView'
import { useAuth } from '../../hooks/useAuth'
import { fmtTime } from '../../utils/format'
import { showSuccess } from '../../utils/feedback'

export default function Scripts() {
  const { can } = useAuth()
  const qc = useQueryClient()
  const [search, setSearch] = useState('')
  const [editing, setEditing] = useState<Script | 'new' | null>(null)
  const { data, isLoading } = useQuery({ queryKey: qk.scripts, queryFn: scriptApi.list })
  const remove = useMutation({
    mutationFn: scriptApi.remove,
    onSuccess: () => {
      showSuccess('已刪除')
      void qc.invalidateQueries({ queryKey: qk.scripts })
    },
  })
  const q = search.trim().toLowerCase()
  const rows = (data ?? []).filter((s) => !q || s.name.toLowerCase().includes(q) || s.description?.toLowerCase().includes(q))

  return (
    <>
      <PageHeader
        title="指令庫"
        subtitle="儲存常用的 PowerShell／CMD 指令碼，可在執行指令與排程中載入"
        extra={
          can('operator') && (
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setEditing('new')}>
              新增指令碼
            </Button>
          )
        }
      />
      <Card>
        <Input.Search allowClear placeholder="搜尋名稱或說明" value={search} onChange={(e) => setSearch(e.target.value)} style={{ maxWidth: 300, marginBottom: 12 }} />
        <Table<Script>
          rowKey="id"
          dataSource={rows}
          loading={isLoading}
          pagination={{ pageSize: 50, hideOnSinglePage: true }}
          expandable={{ expandedRowRender: (s) => <OutputBlock label="內容" text={s.body} /> }}
          scroll={{ x: 800 }}
          columns={[
            { title: '名稱', dataIndex: 'name', width: 220, render: (v: string) => <Typography.Text strong>{v}</Typography.Text> },
            { title: '類型', dataIndex: 'shell', width: 110, render: (v: string) => <Tag color={v === 'powershell' ? 'blue' : 'default'}>{v === 'powershell' ? 'PowerShell' : 'CMD'}</Tag> },
            { title: '說明', dataIndex: 'description', ellipsis: true, render: (v: string) => v || '-' },
            { title: '逾時', dataIndex: 'timeout_sec', width: 90, render: (v: number) => `${v} 秒` },
            { title: '更新時間', dataIndex: 'updated_at', width: 150, render: (v?: string) => fmtTime(v) },
            {
              title: '操作',
              key: 'act',
              width: 100,
              render: (_, r) =>
                can('operator') && (
                  <Space size={0}>
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
      <ScriptDrawer value={editing} onClose={() => setEditing(null)} />
    </>
  )
}

function ScriptDrawer({ value, onClose }: { value: Script | 'new' | null; onClose: () => void }) {
  const [form] = Form.useForm<ScriptInput>()
  const qc = useQueryClient()
  const isNew = value === 'new'
  const m = useMutation({
    mutationFn: (v: ScriptInput) => (isNew || !value ? scriptApi.create(v) : scriptApi.update(value.id, v)),
    onSuccess: () => {
      showSuccess(isNew ? '已新增指令碼' : '已更新指令碼')
      void qc.invalidateQueries({ queryKey: qk.scripts })
      onClose()
    },
  })
  const initial: ScriptInput =
    value && value !== 'new'
      ? { name: value.name, description: value.description, shell: value.shell, body: value.body, timeout_sec: value.timeout_sec }
      : { name: '', description: '', shell: 'powershell', body: '', timeout_sec: 300 }
  return (
    <Drawer
      title={isNew ? '新增指令碼' : '編輯指令碼'}
      open={!!value}
      onClose={onClose}
      size={760}
      destroyOnHidden
      extra={
        <Space>
          <Button onClick={onClose}>取消</Button>
          <Button type="primary" loading={m.isPending} onClick={() => void form.validateFields().then((v) => m.mutate(v))}>
            儲存
          </Button>
        </Space>
      }
    >
      <Form form={form} layout="vertical" initialValues={initial} preserve={false}>
        <Row gutter={12}>
          <Col span={12}>
            <Form.Item label="名稱" name="name" rules={[{ required: true, message: '請輸入名稱' }]}>
              <Input placeholder="清除暫存檔" />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item label="類型" name="shell">
              <Select
                options={[
                  { value: 'powershell', label: 'PowerShell' },
                  { value: 'cmd', label: 'CMD' },
                ]}
              />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item label="逾時（秒）" name="timeout_sec">
              <InputNumber min={10} max={3600} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
        </Row>
        <Form.Item label="說明" name="description">
          <Input.TextArea rows={2} />
        </Form.Item>
        <Form.Item label="內容" name="body" rules={[{ required: true, message: '請輸入指令內容' }]}>
          <CodeArea rows={20} />
        </Form.Item>
      </Form>
    </Drawer>
  )
}
