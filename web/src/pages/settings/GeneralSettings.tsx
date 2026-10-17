import { useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Card, Form, Input, InputNumber, Select, Skeleton } from 'antd'
import { settingsApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { GeneralSettings as GS } from '../../api/types'
import { showSuccess } from '../../utils/feedback'

const timezones = ['Asia/Taipei', 'Asia/Hong_Kong', 'Asia/Shanghai', 'Asia/Tokyo', 'Asia/Singapore', 'UTC', 'America/Los_Angeles', 'Europe/London']

export default function GeneralSettings() {
  const qc = useQueryClient()
  const [form] = Form.useForm<GS>()
  const { data, isLoading } = useQuery({ queryKey: qk.settings('general'), queryFn: settingsApi.getGeneral })
  useEffect(() => {
    if (data) form.setFieldsValue(data)
  }, [data, form])
  const m = useMutation({
    mutationFn: settingsApi.putGeneral,
    onSuccess: (d) => {
      showSuccess('已儲存一般設定')
      qc.setQueryData(qk.settings('general'), d ?? form.getFieldsValue())
    },
  })
  if (isLoading) return <Skeleton active />
  return (
    <Card style={{ maxWidth: 720 }}>
      <Form form={form} layout="vertical" onFinish={(v) => m.mutate(v)}>
        <Form.Item
          label="伺服器網址"
          name="server_url"
          rules={[{ required: true, message: '請輸入伺服器網址' }, { type: 'url', message: '網址格式錯誤' }]}
          extra="Agent 連線與安裝指令使用的對外網址，例如 https://nexmis.corp.local"
        >
          <Input />
        </Form.Item>
        <Form.Item label="時區" name="timezone" extra="排程 Cron 依此時區計算" rules={[{ required: true }]}>
          <Select showSearch options={timezones.map((t) => ({ value: t, label: t }))} />
        </Form.Item>
        <Form.Item label="資料保存天數" name="data_retention_days" extra="健康、電池等時序資料的保存期限" rules={[{ required: true }]}>
          <InputNumber min={7} max={3650} suffix="天" style={{ width: 200 }} />
        </Form.Item>
        <Form.Item label="電池健康警示門檻" name="battery_alert_percent" extra="健康度低於此值會出現在儀表板警示" rules={[{ required: true }]}>
          <InputNumber min={10} max={100} suffix="%" style={{ width: 200 }} />
        </Form.Item>
        <Form.Item label="保固到期提醒" name="warranty_alert_days" extra="保固於此天數內到期會出現在儀表板" rules={[{ required: true }]}>
          <InputNumber min={1} max={730} suffix="天" style={{ width: 200 }} />
        </Form.Item>
        <Button type="primary" htmlType="submit" loading={m.isPending}>
          儲存
        </Button>
      </Form>
    </Card>
  )
}
