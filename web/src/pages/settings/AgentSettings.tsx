import { useEffect } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Divider, Form, InputNumber, Row, Select, Skeleton } from 'antd'
import { settingsApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { AgentConfig } from '../../api/types'
import { consentLabels, toOptions } from '../../utils/labels'
import { fmtDuration } from '../../utils/format'
import { showSuccess } from '../../utils/feedback'
import AgentUpdatePanel from './AgentUpdatePanel'

function SecField({ name, label, min, max, extra }: { name: keyof AgentConfig; label: string; min: number; max: number; extra?: string }) {
  return (
    <Form.Item noStyle shouldUpdate={(a, b) => a[name] !== b[name]}>
      {({ getFieldValue }) => (
        <Form.Item label={label} name={name} rules={[{ required: true }]} extra={`${extra ? extra + '；' : ''}約 ${fmtDuration(Number(getFieldValue(name)) || 0)}`}>
          <InputNumber min={min} max={max} suffix="秒" style={{ width: '100%' }} />
        </Form.Item>
      )}
    </Form.Item>
  )
}

export default function AgentSettings() {
  const qc = useQueryClient()
  const [form] = Form.useForm<AgentConfig>()
  const { data, isLoading } = useQuery({ queryKey: qk.settings('agent'), queryFn: settingsApi.getAgent })
  useEffect(() => {
    if (data) form.setFieldsValue(data)
  }, [data, form])
  const m = useMutation({
    mutationFn: settingsApi.putAgent,
    onSuccess: (d) => {
      showSuccess('已儲存，設定會即時推送至線上的 Agent')
      qc.setQueryData(qk.settings('agent'), d ?? form.getFieldsValue())
    },
  })
  if (isLoading) return <Skeleton active />
  return (
    <>
      <AgentUpdatePanel />
      <Card style={{ maxWidth: 860 }}>
        <Alert type="info" showIcon title="變更會立即推送至所有線上的 Agent；離線 Agent 於下次連線時套用。" style={{ marginBottom: 16 }} />
        <Form form={form} layout="vertical" onFinish={(v) => m.mutate(v)}>
          <Divider titlePlacement="start">回報頻率</Divider>
          <Row gutter={16}>
            <Col xs={24} sm={12} md={8}>
              <SecField name="heartbeat_sec" label="心跳" min={15} max={600} extra="影響線上狀態判定" />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <SecField name="health_sec" label="健康診斷" min={60} max={86400} />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <SecField name="battery_sec" label="電池" min={600} max={604800} />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <SecField name="hardware_sec" label="硬體盤點" min={3600} max={604800} extra="另於開機時收集" />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <SecField name="software_sec" label="軟體盤點" min={600} max={604800} />
            </Col>
            <Col xs={24} sm={12} md={8}>
              <SecField name="update_check_sec" label="檢查 Agent 更新" min={3600} max={604800} extra="另於連線時與發佈新版時檢查" />
            </Col>
          </Row>
          <Divider titlePlacement="start">遠端連線</Divider>
          <Row gutter={16}>
            <Col xs={24} sm={12}>
              <Form.Item label="預設同意政策" name="consent_policy" extra="可由裝置群組覆寫" rules={[{ required: true }]}>
                <Select options={toOptions(consentLabels)} />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="同意逾時" name="consent_timeout_sec" extra="逾時視為拒絕" rules={[{ required: true }]}>
                <InputNumber min={10} max={600} suffix="秒" style={{ width: '100%' }} />
              </Form.Item>
            </Col>
          </Row>
          <Divider titlePlacement="start">重新開機</Divider>
          <Row gutter={16}>
            <Col xs={24} sm={12}>
              <Form.Item label="預設倒數" name="reboot_countdown_sec" rules={[{ required: true }]}>
                <InputNumber min={0} max={86400} suffix="秒" style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="使用者最多可延後次數" name="reboot_max_defer" rules={[{ required: true }]}>
                <InputNumber min={0} max={20} suffix="次" style={{ width: '100%' }} />
              </Form.Item>
            </Col>
          </Row>
          <Button type="primary" htmlType="submit" loading={m.isPending}>
            儲存
          </Button>
        </Form>
      </Card>
    </>
  )
}
