import { useMutation, useQueryClient } from '@tanstack/react-query'
import { DatePicker, Form, Input, Modal, Progress, Switch, Tooltip } from 'antd'
import type { Dayjs } from 'dayjs'
import dayjs from 'dayjs'
import { deploymentApi } from '../api/endpoints'
import { qk } from '../api/keys'
import type { Deployment, NewDeployment, Target } from '../api/types'
import { PackageSelect } from './Selectors'
import { TargetSelector } from './TargetSelector'
import { emptyTarget, targetValidator } from '../utils/target'
import { showSuccess } from '../utils/feedback'

export function DeploymentProgress({ d }: { d: Pick<Deployment, 'total' | 'succeeded' | 'failed' | 'pending'> }) {
  const done = d.succeeded + d.failed
  const pct = d.total ? Math.round((done / d.total) * 100) : 0
  const okPct = d.total ? Math.round((d.succeeded / d.total) * 100) : 0
  return (
    <Tooltip title={`成功 ${d.succeeded}、失敗 ${d.failed}、等待 ${d.pending}，共 ${d.total} 台`}>
      <Progress percent={pct} success={{ percent: okPct }} status={d.failed ? 'exception' : undefined} size="small" format={() => `${done}/${d.total}`} />
    </Tooltip>
  )
}

interface FormValues {
  name: string
  package_id: string
  target: Target
  schedule: boolean
  scheduled_at?: Dayjs
}

export function NewDeploymentModal({
  open,
  onClose,
  onCreated,
  initialTarget,
}: {
  open: boolean
  onClose: () => void
  onCreated?: (id: string) => void
  initialTarget?: Target
}) {
  const [form] = Form.useForm<FormValues>()
  const qc = useQueryClient()
  const schedule = Form.useWatch('schedule', form)
  const m = useMutation({
    mutationFn: (b: NewDeployment) => deploymentApi.create(b),
    onSuccess: (d) => {
      showSuccess('已建立部署工作')
      void qc.invalidateQueries({ queryKey: qk.deployments })
      onClose()
      onCreated?.(d.id)
    },
  })
  const submit = async () => {
    const v = await form.validateFields()
    m.mutate({
      name: v.name,
      package_id: v.package_id,
      target: v.target,
      scheduled_at: v.schedule && v.scheduled_at ? v.scheduled_at.toISOString() : undefined,
    })
  }
  return (
    <Modal title="新增部署" open={open} onCancel={onClose} onOk={() => void submit()} okText="建立" confirmLoading={m.isPending} width={680} destroyOnHidden>
      <Form form={form} layout="vertical" preserve={false} initialValues={{ target: initialTarget ?? emptyTarget(), schedule: false }}>
        <Form.Item label="套件" name="package_id" rules={[{ required: true, message: '請選擇套件' }]}>
          <PackageSelect />
        </Form.Item>
        <Form.Item label="部署名稱" name="name" rules={[{ required: true, message: '請輸入名稱' }]}>
          <Input placeholder="例如：部署 7-Zip 24.08 至全公司" />
        </Form.Item>
        <Form.Item label="目標" name="target" rules={[{ validator: targetValidator }]}>
          <TargetSelector />
        </Form.Item>
        <Form.Item label="排程執行" name="schedule" valuePropName="checked">
          <Switch checkedChildren="指定時間" unCheckedChildren="立即" />
        </Form.Item>
        {schedule && (
          <Form.Item label="執行時間" name="scheduled_at" rules={[{ required: true, message: '請選擇時間' }]}>
            <DatePicker showTime={{ format: 'HH:mm' }} format="YYYY-MM-DD HH:mm" disabledDate={(d) => d.isBefore(dayjs().startOf('day'))} style={{ width: 240 }} />
          </Form.Item>
        )}
      </Form>
    </Modal>
  )
}
