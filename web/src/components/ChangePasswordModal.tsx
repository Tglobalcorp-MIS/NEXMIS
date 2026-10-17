import { Form, Input, Modal } from 'antd'
import { useMutation } from '@tanstack/react-query'
import { authApi } from '../api/endpoints'
import { showSuccess } from '../utils/feedback'

export function ChangePasswordModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [form] = Form.useForm<{ current_password: string; new_password: string; confirm: string }>()
  const m = useMutation({
    mutationFn: authApi.changePassword,
    onSuccess: () => {
      showSuccess('密碼已變更')
      onClose()
    },
  })
  return (
    <Modal
      title="變更密碼"
      open={open}
      onCancel={onClose}
      okText="變更"
      confirmLoading={m.isPending}
      destroyOnHidden
      onOk={async () => {
        const v = await form.validateFields()
        m.mutate({ current_password: v.current_password, new_password: v.new_password })
      }}
    >
      <Form form={form} layout="vertical" preserve={false}>
        <Form.Item label="目前密碼" name="current_password" rules={[{ required: true, message: '請輸入目前密碼' }]}>
          <Input.Password autoComplete="current-password" />
        </Form.Item>
        <Form.Item
          label="新密碼"
          name="new_password"
          rules={[
            { required: true, message: '請輸入新密碼' },
            { min: 10, message: '至少 10 個字元' },
          ]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
        <Form.Item
          label="確認新密碼"
          name="confirm"
          dependencies={['new_password']}
          rules={[
            { required: true, message: '請再次輸入新密碼' },
            ({ getFieldValue }) => ({
              validator: (_, v) =>
                !v || v === getFieldValue('new_password') ? Promise.resolve() : Promise.reject(new Error('兩次輸入不一致')),
            }),
          ]}
        >
          <Input.Password autoComplete="new-password" />
        </Form.Item>
      </Form>
    </Modal>
  )
}
