import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Flex, Form, Input, Modal, Space, Typography } from 'antd'
import { DesktopOutlined, EyeOutlined } from '@ant-design/icons'
import { deviceApi } from '../../../api/endpoints'
import { qk } from '../../../api/keys'
import type { DeviceDetail, RemoteMode } from '../../../api/types'
import { RemoteSessionTable } from '../../../components/RemoteSessionTable'
import { consentLabels, labelOf, remoteModeLabels } from '../../../utils/labels'
import { showError } from '../../../utils/feedback'

export default function RemoteTab({ device }: { device: DeviceDetail }) {
  const qc = useQueryClient()
  const [mode, setMode] = useState<RemoteMode | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [form] = Form.useForm<{ reason: string }>()
  const [page, setPage] = useState(1)
  const params = { page, page_size: 20 }
  const { data, isLoading } = useQuery({
    queryKey: [...qk.remoteSessions, 'device', device.id, params],
    queryFn: () => deviceApi.remoteSessions(device.id, params),
  })
  const items = data?.items

  const canConnect = device.has_agent && device.online && device.consent_policy !== 'deny'

  const submit = async () => {
    if (!mode) return
    const v = await form.validateFields()
    // 先同步開啟視窗以避免被彈出視窗封鎖
    const win = window.open('about:blank', '_blank')
    setSubmitting(true)
    try {
      const s = await deviceApi.requestRemote(device.id, { mode, reason: v.reason })
      if (win) win.location.href = `/remote/${s.id}`
      else window.location.href = `/remote/${s.id}`
      setMode(null)
      form.resetFields()
      void qc.invalidateQueries({ queryKey: qk.remoteSessions })
    } catch (e) {
      win?.close()
      if (e instanceof Error) showError(e.message)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Flex vertical gap={16}>
      <Card title="遠端協助">
        <Typography.Paragraph type="secondary">
          遠端連線需經使用者同意（目前政策：{labelOf(consentLabels, device.consent_policy)}）。連線期間使用者畫面會持續顯示提示，並可隨時中斷；所有連線都會記錄於稽核紀錄。
        </Typography.Paragraph>
        {!device.has_agent && <Alert type="warning" showIcon title="此設備未安裝 Agent，無法遠端連線。" />}
        {device.has_agent && !device.online && <Alert type="warning" showIcon title="裝置目前離線，無法遠端連線。" />}
        {device.has_agent && device.online && !device.helper_online && (
          <Alert type="warning" showIcon title="使用者端助手未執行（可能無人登入），連線請求可能失敗。" style={{ marginBottom: 12 }} />
        )}
        {device.consent_policy === 'deny' && <Alert type="error" showIcon title="此裝置所屬群組禁止遠端連線。" />}
        <Space style={{ marginTop: 12 }} wrap>
          <Button icon={<EyeOutlined />} size="large" disabled={!canConnect} onClick={() => setMode('view')}>
            請求檢視
          </Button>
          <Button type="primary" icon={<DesktopOutlined />} size="large" disabled={!canConnect} onClick={() => setMode('control')}>
            請求操作
          </Button>
        </Space>
      </Card>
      <Card title="此裝置的連線紀錄">
        <RemoteSessionTable
          data={items}
          loading={isLoading}
          showDevice={false}
          pagination={{ current: page, pageSize: 20, total: data?.total ?? 0, onChange: setPage, hideOnSinglePage: true }}
        />
      </Card>
      <Modal
        title={`請求遠端${mode ? remoteModeLabels[mode] : ''}：${device.name}`}
        open={!!mode}
        onCancel={() => setMode(null)}
        onOk={() => void submit()}
        okText="送出請求"
        confirmLoading={submitting}
        destroyOnHidden
      >
        <Typography.Paragraph type="secondary">
          使用者{device.logged_in_user ? `（${device.logged_in_user}）` : ''}會看到您的姓名與以下原因。
          {mode === 'control' && '「可操作」模式允許您控制滑鼠與鍵盤。'}
        </Typography.Paragraph>
        <Form form={form} layout="vertical" preserve={false}>
          <Form.Item label="連線原因" name="reason" rules={[{ required: true, message: '請填寫連線原因' }]}>
            <Input.TextArea rows={3} placeholder="例如：協助排除 Outlook 無法收信問題（工單 #1234）" maxLength={500} showCount />
          </Form.Item>
        </Form>
      </Modal>
    </Flex>
  )
}
