import { useState, type ReactNode } from 'react'
import { Alert, Button, Checkbox, Drawer, Form, Input, InputNumber, Modal, Select, Space } from 'antd'
import type { CommandKind, NewCommand } from '../api/types'
import { CommandEditor } from './CommandEditor'
import { defaultCommandValues, toNewCommand, type CommandFormValues } from '../utils/command'

/** 執行指令抽屜（單台或批次共用）。 */
export function RunCommandDrawer({
  open,
  onClose,
  onSubmit,
  title = '執行指令',
  kinds,
  initial,
  header,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (cmd: NewCommand) => Promise<unknown>
  title?: ReactNode
  kinds?: CommandKind[]
  initial?: Partial<CommandFormValues>
  header?: ReactNode
}) {
  const [form] = Form.useForm<CommandFormValues>()
  const [submitting, setSubmitting] = useState(false)

  const submit = async () => {
    const values = await form.validateFields()
    setSubmitting(true)
    try {
      await onSubmit(toNewCommand(values))
      onClose()
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Drawer
      title={title}
      open={open}
      onClose={onClose}
      size={720}
      destroyOnHidden
      afterOpenChange={(o) => {
        if (o) form.setFieldsValue({ ...defaultCommandValues, ...(kinds && !kinds.includes('powershell') ? { kind: kinds[0] } : {}), ...initial })
      }}
      extra={
        <Space>
          <Button onClick={onClose}>取消</Button>
          <Button type="primary" loading={submitting} onClick={() => void submit()}>
            執行
          </Button>
        </Space>
      }
    >
      {header}
      <Form form={form} layout="vertical" initialValues={{ ...defaultCommandValues, ...initial }}>
        <CommandEditor form={form} kinds={kinds} />
      </Form>
    </Drawer>
  )
}

export interface RebootFormValues {
  countdown_sec: number
  message: string
  allow_defer: boolean
  force: boolean
}

/** 安全重新開機／關機對話框。 */
export function RebootModal({
  open,
  onClose,
  onSubmit,
  kind = 'reboot',
  description,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (cmd: NewCommand) => Promise<unknown>
  kind?: 'reboot' | 'shutdown'
  description?: ReactNode
}) {
  const [form] = Form.useForm<RebootFormValues>()
  const [submitting, setSubmitting] = useState(false)
  const label = kind === 'reboot' ? '重新開機' : '關機'
  const force = Form.useWatch('force', form)

  const submit = async () => {
    const v = await form.validateFields()
    setSubmitting(true)
    try {
      await onSubmit({
        kind,
        timeout_sec: Math.max(300, (v.countdown_sec ?? 0) + 600),
        args: { countdown_sec: v.countdown_sec, message: v.message ?? '', allow_defer: v.allow_defer, force: v.force },
      })
      onClose()
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Modal
      title={`安全${label}`}
      open={open}
      onCancel={onClose}
      onOk={() => void submit()}
      okText={`確認${label}`}
      okButtonProps={{ danger: true, loading: submitting }}
      destroyOnHidden
    >
      {description}
      <Form
        form={form}
        layout="vertical"
        preserve={false}
        initialValues={{
          countdown_sec: 300,
          allow_defer: true,
          force: false,
          message: `MIS 將於倒數結束後${label}，請儲存您的工作。`,
        }}
      >
        <Form.Item label="倒數秒數" name="countdown_sec" rules={[{ required: true }]} extra="使用者畫面會顯示倒數；無人登入時立即執行。">
          <InputNumber min={0} max={86400} step={60} style={{ width: '100%' }} suffix="秒" />
        </Form.Item>
        <Form.Item label="通知訊息" name="message">
          <Input.TextArea rows={2} />
        </Form.Item>
        <Form.Item name="allow_defer" valuePropName="checked" style={{ marginBottom: 8 }}>
          <Checkbox>允許使用者延後（依 Agent 設定的延後次數上限）</Checkbox>
        </Form.Item>
        <Form.Item name="force" valuePropName="checked">
          <Checkbox>強制{label}（不等待應用程式關閉）</Checkbox>
        </Form.Item>
        {force && <Alert type="warning" showIcon title="強制執行可能導致使用者未儲存的資料遺失。" />}
      </Form>
    </Modal>
  )
}

export function NotifyModal({
  open,
  onClose,
  onSubmit,
}: {
  open: boolean
  onClose: () => void
  onSubmit: (cmd: NewCommand) => Promise<unknown>
}) {
  const [form] = Form.useForm<{ title: string; message: string; level: 'info' | 'warning' | 'critical' }>()
  const [submitting, setSubmitting] = useState(false)
  const submit = async () => {
    const v = await form.validateFields()
    setSubmitting(true)
    try {
      await onSubmit({ kind: 'notify', timeout_sec: 300, args: { title: v.title, message: v.message, level: v.level } })
      onClose()
    } finally {
      setSubmitting(false)
    }
  }
  return (
    <Modal
      title="傳送通知"
      open={open}
      onCancel={onClose}
      onOk={() => void submit()}
      okText="傳送"
      okButtonProps={{ loading: submitting }}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" preserve={false} initialValues={{ title: 'MIS 通知', level: 'info' }}>
        <Form.Item label="標題" name="title" rules={[{ required: true, message: '請輸入標題' }]}>
          <Input />
        </Form.Item>
        <Form.Item label="內容" name="message" rules={[{ required: true, message: '請輸入內容' }]}>
          <Input.TextArea rows={3} />
        </Form.Item>
        <Form.Item label="等級" name="level">
          <Select
            options={[
              { value: 'info', label: '資訊' },
              { value: 'warning', label: '警告' },
              { value: 'critical', label: '重要' },
            ]}
          />
        </Form.Item>
      </Form>
    </Modal>
  )
}
