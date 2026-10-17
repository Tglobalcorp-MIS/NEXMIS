import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Card, Form, Input, Modal, Popconfirm, Select, Space, Switch, Table, Tag, Typography } from 'antd'
import { DeleteOutlined, EditOutlined, PlusOutlined } from '@ant-design/icons'
import { userApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { NewUser, Role, User, UserPatch } from '../../api/types'
import { RoleTag } from '../../components/StatusTags'
import { useAuth } from '../../hooks/useAuth'
import { fmtTime } from '../../utils/format'
import { roleLabels, toOptions } from '../../utils/labels'
import { showSuccess } from '../../utils/feedback'

const roleHelp: Record<Role, string> = {
  admin: '全部功能，包含系統設定與稽核',
  operator: '指令、部署、遠端、資產管理',
  helpdesk: '遠端協助、訊息、重新整理／通知／鎖定',
  viewer: '唯讀',
}

export default function Users() {
  const { user: me } = useAuth()
  const qc = useQueryClient()
  const [editing, setEditing] = useState<User | 'new' | null>(null)
  const { data, isLoading } = useQuery({ queryKey: qk.users, queryFn: userApi.list })
  const invalidate = () => void qc.invalidateQueries({ queryKey: qk.users })
  const toggle = useMutation({ mutationFn: (u: User) => userApi.update(u.id, { disabled: !u.disabled }), onSuccess: invalidate })
  const remove = useMutation({ mutationFn: userApi.remove, onSuccess: () => { showSuccess('已刪除使用者'); invalidate() } })

  return (
    <Card
      title="使用者"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setEditing('new')}>
          新增本機帳號
        </Button>
      }
    >
      <Typography.Paragraph type="secondary">AD 帳號於首次登入時自動建立，角色依「AD 整合」中的群組對應決定；本機帳號可手動指定角色。</Typography.Paragraph>
      <Table<User>
        rowKey="id"
        dataSource={data}
        loading={isLoading}
        pagination={{ pageSize: 50, hideOnSinglePage: true }}
        scroll={{ x: 900 }}
        columns={[
          {
            title: '帳號',
            dataIndex: 'username',
            render: (v: string, u) => (
              <div>
                <Typography.Text strong>{u.display_name || v}</Typography.Text>
                <div>
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {v}
                    {u.email ? ` · ${u.email}` : ''}
                  </Typography.Text>
                </div>
              </div>
            ),
          },
          { title: '來源', dataIndex: 'source', width: 90, render: (v: string) => <Tag color={v === 'ad' ? 'blue' : 'default'}>{v === 'ad' ? 'AD' : '本機'}</Tag> },
          { title: '角色', dataIndex: 'role', width: 120, render: (r: Role) => <RoleTag role={r} /> },
          {
            title: '啟用',
            dataIndex: 'disabled',
            width: 80,
            render: (v: boolean, u) => <Switch size="small" checked={!v} disabled={u.id === me?.id} loading={toggle.isPending && toggle.variables?.id === u.id} onChange={() => toggle.mutate(u)} />,
          },
          { title: '最後登入', dataIndex: 'last_login_at', width: 150, render: (v: string | null) => fmtTime(v) },
          { title: '建立時間', dataIndex: 'created_at', width: 150, render: (v: string) => fmtTime(v) },
          {
            title: '操作',
            key: 'act',
            width: 100,
            render: (_, u) => (
              <Space size={0}>
                <Button type="link" size="small" icon={<EditOutlined />} onClick={() => setEditing(u)} aria-label="編輯" />
                {u.id !== me?.id && (
                  <Popconfirm title={`刪除使用者「${u.username}」？`} okButtonProps={{ danger: true }} onConfirm={() => remove.mutateAsync(u.id)}>
                    <Button type="link" size="small" danger icon={<DeleteOutlined />} aria-label="刪除" />
                  </Popconfirm>
                )}
              </Space>
            ),
          },
        ]}
      />
      <UserModal value={editing} onClose={() => setEditing(null)} />
    </Card>
  )
}

function UserModal({ value, onClose }: { value: User | 'new' | null; onClose: () => void }) {
  const [form] = Form.useForm<NewUser>()
  const qc = useQueryClient()
  const isNew = value === 'new'
  const isAD = value !== 'new' && value?.source === 'ad'
  const role = Form.useWatch('role', form) as Role | undefined
  const m = useMutation({
    mutationFn: (v: NewUser) => {
      if (isNew || !value) return userApi.create(v)
      const patch: UserPatch = { display_name: v.display_name, email: v.email, role: v.role }
      if (v.password) patch.password = v.password
      return userApi.update(value.id, patch)
    },
    onSuccess: () => {
      showSuccess(isNew ? '已新增使用者' : '已更新使用者')
      void qc.invalidateQueries({ queryKey: qk.users })
      onClose()
    },
  })
  const initial = value && value !== 'new' ? { ...value, password: '' } : { role: 'viewer' as Role }
  return (
    <Modal title={isNew ? '新增本機帳號' : '編輯使用者'} open={!!value} onCancel={onClose} confirmLoading={m.isPending} destroyOnHidden onOk={() => void form.validateFields().then((v) => m.mutate(v))}>
      <Form form={form} layout="vertical" preserve={false} initialValues={initial}>
        <Form.Item label="帳號" name="username" rules={[{ required: true, message: '請輸入帳號' }, { pattern: /^[A-Za-z0-9._-]+$/, message: '只能使用英數字與 . _ -' }]}>
          <Input disabled={!isNew} autoComplete="off" />
        </Form.Item>
        <Form.Item label="顯示名稱" name="display_name">
          <Input disabled={isAD} />
        </Form.Item>
        <Form.Item label="電子郵件" name="email" rules={[{ type: 'email', message: '格式錯誤' }]}>
          <Input disabled={isAD} />
        </Form.Item>
        <Form.Item label="角色" name="role" rules={[{ required: true }]} extra={role ? roleHelp[role] : undefined}>
          <Select options={toOptions(roleLabels)} />
        </Form.Item>
        {!isAD && (
          <Form.Item
            label={isNew ? '密碼' : '重設密碼'}
            name="password"
            rules={[{ required: isNew, message: '請輸入密碼' }, { min: 10, message: '至少 10 個字元' }]}
            extra={isNew ? undefined : '留空表示不變更'}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
        )}
      </Form>
    </Modal>
  )
}
