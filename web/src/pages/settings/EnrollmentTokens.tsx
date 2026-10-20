import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, DatePicker, Form, Input, InputNumber, Modal, Popconfirm, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import type { Dayjs } from 'dayjs'
import dayjs from 'dayjs'
import { settingsApi, tokenApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { CreatedEnrollmentToken, EnrollmentToken, NewEnrollmentToken } from '../../api/types'
import { GroupSelect, TagsInput } from '../../components/Selectors'
import { OutputBlock } from '../../components/JsonView'
import { useGroupName } from '../../hooks/useGroups'
import { fmtTime } from '../../utils/format'
import { showSuccess } from '../../utils/feedback'

function tokenState(t: EnrollmentToken): { text: string; color: string } {
  if (t.revoked) return { text: '已撤銷', color: 'default' }
  if (t.expires_at && dayjs(t.expires_at).isBefore(dayjs())) return { text: '已過期', color: 'default' }
  if (t.max_uses !== null && t.uses >= t.max_uses) return { text: '已用完', color: 'default' }
  return { text: '有效', color: 'success' }
}

export default function EnrollmentTokens() {
  const qc = useQueryClient()
  const groupName = useGroupName()
  const [open, setOpen] = useState(false)
  const [created, setCreated] = useState<CreatedEnrollmentToken | null>(null)
  const { data, isLoading } = useQuery({ queryKey: qk.tokens, queryFn: tokenApi.list })
  const revoke = useMutation({
    mutationFn: tokenApi.revoke,
    onSuccess: () => {
      showSuccess('已撤銷權杖')
      void qc.invalidateQueries({ queryKey: qk.tokens })
    },
  })

  return (
    <Card
      title="Agent 註冊權杖"
      extra={
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>
          建立權杖
        </Button>
      }
    >
      <Typography.Paragraph type="secondary">
        安裝 Agent 時需提供註冊權杖。可設定期限、使用次數上限，並自動將新裝置加入群組與標籤。權杖明文僅在建立時顯示一次。
      </Typography.Paragraph>
      <Table<EnrollmentToken>
        rowKey="id"
        dataSource={data}
        loading={isLoading}
        pagination={{ pageSize: 50, hideOnSinglePage: true }}
        scroll={{ x: 900 }}
        columns={[
          { title: '名稱', dataIndex: 'name' },
          { title: '前綴', dataIndex: 'token_prefix', width: 120, render: (v: string) => <span className="mono">{v}…</span> },
          { title: '狀態', key: 'state', width: 90, render: (_, t) => <Tag color={tokenState(t).color}>{tokenState(t).text}</Tag> },
          { title: '使用次數', key: 'uses', width: 100, render: (_, t) => `${t.uses} / ${t.max_uses ?? '∞'}` },
          { title: '到期', dataIndex: 'expires_at', width: 140, render: (v: string | null) => (v ? fmtTime(v) : '永不') },
          { title: '群組', dataIndex: 'group_id', width: 120, render: (v: string | null) => (v ? (groupName(v) ?? v.slice(0, 8)) : '-') },
          { title: '標籤', dataIndex: 'tags', width: 140, render: (v: string[] | null) => v?.map((t) => <Tag key={t}>{t}</Tag>) ?? '-' },
          { title: '建立者', dataIndex: 'created_by_name', width: 100, render: (v: string | null) => v ?? '-' },
          { title: '建立時間', dataIndex: 'created_at', width: 140, render: (v: string) => fmtTime(v) },
          {
            title: '',
            key: 'act',
            width: 80,
            render: (_, t) =>
              !t.revoked && (
                <Popconfirm title="撤銷此權杖？已註冊的裝置不受影響。" okButtonProps={{ danger: true }} onConfirm={() => revoke.mutateAsync(t.id)}>
                  <Button type="link" size="small" danger>
                    撤銷
                  </Button>
                </Popconfirm>
              ),
          },
        ]}
      />
      <CreateTokenModal
        open={open}
        onClose={() => setOpen(false)}
        onCreated={(t) => {
          setOpen(false)
          setCreated(t)
        }}
      />
      <CreatedTokenModal token={created} onClose={() => setCreated(null)} />
    </Card>
  )
}

function CreateTokenModal({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (t: CreatedEnrollmentToken) => void }) {
  const [form] = Form.useForm<{ name: string; expires_at?: Dayjs; max_uses?: number; group_id?: string; tags?: string[] }>()
  const qc = useQueryClient()
  const m = useMutation({
    mutationFn: (b: NewEnrollmentToken) => tokenApi.create(b),
    onSuccess: (t) => {
      void qc.invalidateQueries({ queryKey: qk.tokens })
      onCreated(t)
    },
  })
  return (
    <Modal
      title="建立註冊權杖"
      open={open}
      onCancel={onClose}
      confirmLoading={m.isPending}
      okText="建立"
      destroyOnHidden
      onOk={() =>
        void form.validateFields().then((v) =>
          m.mutate({
            name: v.name,
            expires_at: v.expires_at ? v.expires_at.endOf('day').toISOString() : undefined,
            max_uses: v.max_uses || undefined,
            group_id: v.group_id || undefined,
            tags: v.tags?.length ? v.tags : undefined,
          }),
        )
      }
    >
      <Form form={form} layout="vertical" preserve={false}>
        <Form.Item label="名稱" name="name" rules={[{ required: true, message: '請輸入名稱' }]}>
          <Input placeholder="例如：總部大量部署" />
        </Form.Item>
        <Form.Item label="到期日（選填）" name="expires_at">
          <DatePicker disabledDate={(d) => d.isBefore(dayjs().startOf('day'))} style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item label="使用次數上限（選填）" name="max_uses">
          <InputNumber min={1} style={{ width: '100%' }} placeholder="不限" />
        </Form.Item>
        <Form.Item label="自動加入群組（選填）" name="group_id">
          <GroupSelect />
        </Form.Item>
        <Form.Item label="自動加上標籤（選填）" name="tags">
          <TagsInput />
        </Form.Item>
      </Form>
    </Modal>
  )
}

function CreatedTokenModal({ token, onClose }: { token: CreatedEnrollmentToken | null; onClose: () => void }) {
  const { data: general } = useQuery({ queryKey: qk.settings('general'), queryFn: settingsApi.getGeneral, enabled: !!token })
  const server = general?.server_url || window.location.origin
  const pin = token?.install_pin
  const gpoCommand = `powershell -NoProfile -ExecutionPolicy Bypass -File install-agent.ps1 -Server ${server} -Token ${token?.token}${pin ? ` -PinSHA256 ${pin}` : ''}`
  const exeCommand = `nexmis-agent.exe install --server ${server} --token ${token?.token}${pin ? ` --pin ${pin}` : ''}`
  return (
    <Modal
      title="權杖已建立"
      open={!!token}
      onCancel={onClose}
      footer={
        <Button type="primary" onClick={onClose}>
          我已妥善保存
        </Button>
      }
      width={760}
      mask={{ closable: false }}
      destroyOnHidden
    >
      {token && (
        <>
          <Alert type="warning" showIcon title="權杖明文只會顯示這一次，關閉後無法再次查看，請立即複製保存。" style={{ marginBottom: 16 }} />
          <Typography.Title level={5}>註冊權杖</Typography.Title>
          <Typography.Paragraph copyable={{ text: token.token }} className="mono" style={{ fontSize: 15 }}>
            {token.token}
          </Typography.Paragraph>
          {token.install_command ? (
            <>
              <Typography.Title level={5}>
                安裝指令（PowerShell 或命令提示字元，以系統管理員執行）{' '}
                <Typography.Text copyable={{ text: token.install_command }} />
              </Typography.Title>
              <OutputBlock label="" text={token.install_command} />
              {pin ? (
                <Alert
                  type="info"
                  showIcon
                  title={`伺服器憑證未受系統信任（例如自簽），以下指令皆已帶入憑證 SHA-256 指紋：${pin}`}
                  style={{ marginBottom: 16 }}
                />
              ) : (
                <Alert
                  type="info"
                  showIcon
                  title="若由反向代理以自簽憑證提供 HTTPS，伺服器無法得知該憑證，單行指令會因 TLS 驗證失敗；請改用 install-agent.ps1 並加上 -PinSHA256 <憑證 SHA-256 指紋>。"
                  style={{ marginBottom: 16 }}
                />
              )}
            </>
          ) : (
            <Alert
              type="info"
              showIcon
              title="伺服器網址不是 https://，Agent 會拒絕連線，因此不產生安裝指令。請到「系統設定 → 一般」設定 https 網址。"
              style={{ marginBottom: 16 }}
            />
          )}
          <Typography.Title level={5}>
            GPO 開機指令碼／Intune <Typography.Text copyable={{ text: gpoCommand }} />
          </Typography.Title>
          <OutputBlock label="" text={gpoCommand} />
          <Typography.Title level={5}>
            執行檔安裝 <Typography.Text copyable={{ text: exeCommand }} />
          </Typography.Title>
          <OutputBlock label="" text={exeCommand} />
        </>
      )}
    </Modal>
  )
}
