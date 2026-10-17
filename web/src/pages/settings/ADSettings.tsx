import { useEffect, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Col, Divider, Flex, Form, Input, InputNumber, Row, Select, Skeleton, Space, Switch, Tag, Typography } from 'antd'
import { ApiOutlined, DeleteOutlined, PlusOutlined, SyncOutlined } from '@ant-design/icons'
import { settingsApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { ADSettingsInput, ADTestResult } from '../../api/types'
import { roleLabels, toOptions } from '../../utils/labels'
import { showSuccess } from '../../utils/feedback'

export default function ADSettings() {
  const qc = useQueryClient()
  const [form] = Form.useForm<ADSettingsInput>()
  const [testResult, setTestResult] = useState<ADTestResult | null>(null)
  const { data, isLoading } = useQuery({ queryKey: qk.settings('ad'), queryFn: settingsApi.getAD })
  const enabled = Form.useWatch('enabled', form)

  useEffect(() => {
    if (data) form.setFieldsValue({ ...data, bind_password: '', role_mappings: data.role_mappings ?? [] })
  }, [data, form])

  const clean = (v: ADSettingsInput): ADSettingsInput => {
    const out = { ...v, role_mappings: (v.role_mappings ?? []).filter((r) => r.group_dn) }
    if (!out.bind_password) delete out.bind_password
    return out
  }

  const save = useMutation({
    mutationFn: (v: ADSettingsInput) => settingsApi.putAD(clean(v)),
    onSuccess: () => {
      showSuccess('已儲存 AD 設定')
      void qc.invalidateQueries({ queryKey: qk.settings('ad') })
    },
  })
  const test = useMutation({
    mutationFn: () => settingsApi.testAD(clean(form.getFieldsValue(true) as ADSettingsInput)),
    onMutate: () => setTestResult(null),
    onSuccess: setTestResult,
  })
  const sync = useMutation({
    mutationFn: settingsApi.syncAD,
    onSuccess: (r) => {
      showSuccess(`同步完成：新增 ${r.added}、更新 ${r.updated}，共 ${r.total} 台電腦`)
      void qc.invalidateQueries({ queryKey: qk.devices })
    },
  })

  if (isLoading) return <Skeleton active />

  return (
    <Card style={{ maxWidth: 960 }}>
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        title="NEXMIS 僅以唯讀服務帳號透過 LDAPS 查詢 AD，不寫入 AD、不修改 GPO，也不需要 Domain Admin 權限。"
      />
      <Form form={form} layout="vertical" onFinish={(v) => save.mutate(v)}>
        <Form.Item label="啟用 AD 整合" name="enabled" valuePropName="checked">
          <Switch />
        </Form.Item>
        <Row gutter={16}>
          <Col xs={24} md={12}>
            <Form.Item label="LDAP 伺服器" name="url" rules={[{ required: enabled, message: '請輸入伺服器網址' }]} extra="建議使用 ldaps://dc01.corp.local:636">
              <Input placeholder="ldaps://dc01.corp.local:636" />
            </Form.Item>
          </Col>
          <Col xs={12} md={6}>
            <Form.Item label="網域" name="domain">
              <Input placeholder="corp.local" />
            </Form.Item>
          </Col>
          <Col xs={12} md={6}>
            <Form.Item label="NetBIOS 名稱" name="netbios">
              <Input placeholder="CORP" />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item label="Base DN" name="base_dn" rules={[{ required: enabled, message: '請輸入 Base DN' }]}>
              <Input placeholder="DC=corp,DC=local" />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item label="同步間隔（分鐘）" name="sync_interval_min" extra="最短 30 分鐘">
              <InputNumber min={30} max={10080} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item label="綁定帳號 DN" name="bind_dn">
              <Input placeholder="CN=svc-nexmis,OU=Service,DC=corp,DC=local" />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item
              label={
                <Space>
                  綁定密碼 {data?.bind_password_set && <Tag color="success">已設定</Tag>}
                </Space>
              }
              name="bind_password"
              extra={data?.bind_password_set ? '留空表示不變更' : undefined}
            >
              <Input.Password autoComplete="new-password" placeholder={data?.bind_password_set ? '••••••••' : ''} />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item label="電腦物件 OU" name="computer_base_dn">
              <Input placeholder="OU=Computers,DC=corp,DC=local" />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item label="使用者搜尋 Base DN" name="user_base_dn">
              <Input placeholder="DC=corp,DC=local" />
            </Form.Item>
          </Col>
          <Col xs={24} md={12}>
            <Form.Item label="略過憑證驗證" name="insecure_skip_verify" valuePropName="checked" extra="僅供測試，正式環境請提供 CA 憑證">
              <Switch />
            </Form.Item>
          </Col>
          <Col span={24}>
            <Form.Item label="企業 CA 憑證（PEM）" name="ca_cert_pem">
              <Input.TextArea rows={4} className="mono" placeholder="-----BEGIN CERTIFICATE-----" />
            </Form.Item>
          </Col>
        </Row>

        <Divider titlePlacement="start">AD 群組對應角色</Divider>
        <Typography.Paragraph type="secondary">AD 帳號登入時，依所屬群組決定 NEXMIS 角色；符合多個群組時取最高權限。不屬於任何對應群組的帳號無法登入。</Typography.Paragraph>
        <Form.List name="role_mappings">
          {(fields, { add, remove }) => (
            <>
              {fields.map((f) => (
                <Flex key={f.key} gap={8} align="start">
                  <Form.Item name={[f.name, 'group_dn']} style={{ flex: 1 }} rules={[{ required: true, message: '請輸入群組 DN' }]}>
                    <Input placeholder="CN=NEXMIS-Admins,OU=Groups,DC=corp,DC=local" />
                  </Form.Item>
                  <Form.Item name={[f.name, 'role']} style={{ width: 160 }} rules={[{ required: true }]}>
                    <Select options={toOptions(roleLabels)} />
                  </Form.Item>
                  <Button icon={<DeleteOutlined />} danger onClick={() => remove(f.name)} aria-label="移除" />
                </Flex>
              ))}
              <Button type="dashed" icon={<PlusOutlined />} onClick={() => add({ group_dn: '', role: 'viewer' })} style={{ marginBottom: 24 }}>
                新增對應
              </Button>
            </>
          )}
        </Form.List>

        {testResult && (
          <Alert
            type={testResult.ok ? 'success' : 'error'}
            showIcon
            style={{ marginBottom: 16 }}
            title={testResult.ok ? '連線成功' : '連線失敗'}
            description={
              <>
                <div>{testResult.message}</div>
                {!!testResult.sample_computers?.length && (
                  <div style={{ marginTop: 8 }}>
                    範例電腦：
                    {testResult.sample_computers.map((c) => (
                      <Tag key={c}>{c}</Tag>
                    ))}
                  </div>
                )}
              </>
            }
          />
        )}
        <Space wrap>
          <Button type="primary" htmlType="submit" loading={save.isPending}>
            儲存
          </Button>
          <Button icon={<ApiOutlined />} loading={test.isPending} onClick={() => test.mutate()}>
            測試連線
          </Button>
          <Button icon={<SyncOutlined />} loading={sync.isPending} disabled={!data?.enabled} onClick={() => sync.mutate()}>
            立即同步電腦
          </Button>
        </Space>
      </Form>
    </Card>
  )
}
