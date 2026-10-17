import { useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router'
import { Button, Card, Form, Input, Modal, Popconfirm, Select, Space, Table, Tag } from 'antd'
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons'
import { groupApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { ConsentPolicy, Group, GroupInput } from '../../api/types'
import { useGroups } from '../../hooks/useGroups'
import { consentLabels, toOptions } from '../../utils/labels'
import { showSuccess } from '../../utils/feedback'

const consentColors: Record<ConsentPolicy, string> = { always_ask: 'blue', notify_only: 'orange', deny: 'red' }

export default function Groups() {
  const qc = useQueryClient()
  const [editing, setEditing] = useState<Group | 'new' | null>(null)
  const { data, isLoading } = useGroups()
  const remove = useMutation({
    mutationFn: groupApi.remove,
    onSuccess: () => {
      showSuccess('已刪除群組')
      void qc.invalidateQueries({ queryKey: qk.groups })
    },
  })
  return (
    <Card
      title="裝置群組"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setEditing('new')}>
          新增群組
        </Button>
      }
    >
      <Table<Group>
        rowKey="id"
        dataSource={data}
        loading={isLoading}
        pagination={{ pageSize: 50, hideOnSinglePage: true }}
        columns={[
          { title: '名稱', dataIndex: 'name' },
          { title: '說明', dataIndex: 'description', ellipsis: true, render: (v: string) => v || '-' },
          { title: '遠端同意政策', dataIndex: 'consent_policy', width: 180, render: (v: ConsentPolicy) => <Tag color={consentColors[v]}>{consentLabels[v] ?? v}</Tag> },
          { title: '裝置數', dataIndex: 'device_count', width: 90, render: (v: number, g) => <Link to={`/devices?group_id=${g.id}`}>{v}</Link> },
          {
            title: '操作',
            key: 'act',
            width: 100,
            render: (_, g) => (
              <Space size={0}>
                <Button type="link" size="small" icon={<EditOutlined />} onClick={() => setEditing(g)} aria-label="編輯" />
                <Popconfirm title={`刪除群組「${g.name}」？`} description="群組內的裝置將變為無群組。" okButtonProps={{ danger: true }} onConfirm={() => remove.mutateAsync(g.id)}>
                  <Button type="link" size="small" danger icon={<DeleteOutlined />} aria-label="刪除" />
                </Popconfirm>
              </Space>
            ),
          },
        ]}
      />
      <GroupModal value={editing} onClose={() => setEditing(null)} />
    </Card>
  )
}

function GroupModal({ value, onClose }: { value: Group | 'new' | null; onClose: () => void }) {
  const [form] = Form.useForm<GroupInput>()
  const qc = useQueryClient()
  const isNew = value === 'new'
  const m = useMutation({
    mutationFn: (v: GroupInput) => (isNew || !value ? groupApi.create(v) : groupApi.update(value.id, v)),
    onSuccess: () => {
      showSuccess(isNew ? '已新增群組' : '已更新群組')
      void qc.invalidateQueries({ queryKey: qk.groups })
      void qc.invalidateQueries({ queryKey: qk.devices })
      onClose()
    },
  })
  const initial: GroupInput = value && value !== 'new' ? { name: value.name, description: value.description, consent_policy: value.consent_policy } : { name: '', description: '', consent_policy: 'always_ask' }
  return (
    <Modal title={isNew ? '新增群組' : '編輯群組'} open={!!value} onCancel={onClose} confirmLoading={m.isPending} destroyOnHidden onOk={() => void form.validateFields().then((v) => m.mutate(v))}>
      <Form form={form} layout="vertical" preserve={false} initialValues={initial}>
        <Form.Item label="名稱" name="name" rules={[{ required: true, message: '請輸入名稱' }]}>
          <Input placeholder="財務部" />
        </Form.Item>
        <Form.Item label="說明" name="description">
          <Input.TextArea rows={2} />
        </Form.Item>
        <Form.Item label="遠端同意政策" name="consent_policy" extra="「僅通知」適用無人值守的公用電腦">
          <Select options={toOptions(consentLabels)} />
        </Form.Item>
      </Form>
    </Modal>
  )
}
