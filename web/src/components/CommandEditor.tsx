import { useQuery } from '@tanstack/react-query'
import { Checkbox, Col, Form, Input, InputNumber, Row, Select, Space, Typography } from 'antd'
import type { FormInstance } from 'antd'
import { scriptApi } from '../api/endpoints'
import { qk } from '../api/keys'
import type { CommandKind } from '../api/types'
import { editableKinds, type CommandFormValues } from '../utils/command'
import { commandKindLabels } from '../utils/labels'
import { CodeArea } from './CodeArea'

const required = (msg: string) => [{ required: true, message: msg }]

/**
 * 指令編輯欄位（需放在 antd Form 之內）。
 * `prefix` 可將欄位放在巢狀名稱下（例如排程的 ['command']）。
 */
export function CommandEditor({
  form,
  prefix = [],
  kinds = editableKinds,
}: {
  form: FormInstance
  prefix?: (string | number)[]
  kinds?: CommandKind[]
}) {
  const name = (k: keyof CommandFormValues) => [...prefix, k]
  const kind = Form.useWatch(name('kind'), form) as CommandKind | undefined
  const { data: scripts } = useQuery({ queryKey: qk.scripts, queryFn: scriptApi.list, staleTime: 60_000 })
  const isScript = kind === 'powershell' || kind === 'cmd'

  const loadScript = (id: string) => {
    const sc = scripts?.find((x) => x.id === id)
    if (!sc) return
    form.setFieldValue(name('kind'), sc.shell)
    form.setFieldValue(name('script'), sc.body)
    form.setFieldValue(name('timeout_sec'), sc.timeout_sec || 300)
  }

  return (
    <>
      <Row gutter={12}>
        <Col xs={24} sm={12}>
          <Form.Item label="指令類型" name={name('kind')} rules={required('請選擇指令類型')}>
            <Select options={kinds.map((k) => ({ value: k, label: commandKindLabels[k] }))} />
          </Form.Item>
        </Col>
        <Col xs={24} sm={12}>
          <Form.Item label="逾時（秒）" name={name('timeout_sec')} tooltip="上限 3600 秒">
            <InputNumber min={10} max={3600} step={30} style={{ width: '100%' }} />
          </Form.Item>
        </Col>
      </Row>

      {isScript && (
        <>
          {!!scripts?.length && (
            <Form.Item label="從指令庫載入">
              <Select
                placeholder="選擇已儲存的指令碼"
                showSearch={{ optionFilterProp: 'label' }}
                allowClear
                options={scripts
                  .filter((s) => kinds.includes(s.shell))
                  .map((s) => ({ value: s.id, label: `${s.name}（${commandKindLabels[s.shell]}）` }))}
                onChange={(v?: string) => v && loadScript(v)}
              />
            </Form.Item>
          )}
          <Form.Item
            label={kind === 'powershell' ? 'PowerShell 指令碼' : 'CMD 指令'}
            name={name('script')}
            rules={required('請輸入指令內容')}
            extra="以 SYSTEM 身分執行；輸出上限 1 MB。"
          >
            <CodeArea rows={14} placeholder={kind === 'powershell' ? 'Get-Process | Sort-Object CPU -Descending | Select-Object -First 10' : 'ipconfig /all'} />
          </Form.Item>
        </>
      )}

      {(kind === 'reboot' || kind === 'shutdown') && (
        <>
          <Row gutter={12}>
            <Col xs={24} sm={12}>
              <Form.Item label="倒數秒數" name={name('countdown_sec')} extra="使用者會看到倒數通知；無人登入時立即執行。">
                <InputNumber min={0} max={86400} step={60} style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label=" " colon={false}>
                <Space orientation="vertical">
                  <Form.Item name={name('allow_defer')} valuePropName="checked" noStyle>
                    <Checkbox>允許使用者延後</Checkbox>
                  </Form.Item>
                  <Form.Item name={name('force')} valuePropName="checked" noStyle>
                    <Checkbox>強制（不等待應用程式關閉）</Checkbox>
                  </Form.Item>
                </Space>
              </Form.Item>
            </Col>
          </Row>
          <Form.Item label="通知訊息" name={name('message')}>
            <Input.TextArea rows={2} placeholder="例如：MIS 將於 5 分鐘後重新開機以完成更新，請儲存您的工作。" />
          </Form.Item>
        </>
      )}

      {kind === 'notify' && (
        <>
          <Row gutter={12}>
            <Col xs={24} sm={16}>
              <Form.Item label="標題" name={name('notify_title')} rules={required('請輸入標題')}>
                <Input placeholder="MIS 通知" />
              </Form.Item>
            </Col>
            <Col xs={24} sm={8}>
              <Form.Item label="等級" name={name('notify_level')}>
                <Select
                  options={[
                    { value: 'info', label: '資訊' },
                    { value: 'warning', label: '警告' },
                    { value: 'critical', label: '重要' },
                  ]}
                />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item label="內容" name={name('notify_message')} rules={required('請輸入內容')}>
            <Input.TextArea rows={3} />
          </Form.Item>
        </>
      )}

      {kind === 'service.restart' && (
        <Form.Item label="服務名稱" name={name('service_name')} rules={required('請輸入服務名稱')}>
          <Input placeholder="例如 Spooler" />
        </Form.Item>
      )}

      {kind === 'process.kill' && (
        <Row gutter={12}>
          <Col xs={24} sm={14}>
            <Form.Item label="處理程序名稱" name={name('process_name')}>
              <Input placeholder="例如 notepad.exe" />
            </Form.Item>
          </Col>
          <Col xs={24} sm={10}>
            <Form.Item label="或 PID" name={name('process_pid')}>
              <InputNumber min={1} style={{ width: '100%' }} />
            </Form.Item>
          </Col>
        </Row>
      )}

      {(kind === 'refresh' || kind === 'lock' || kind === 'logoff') && (
        <Typography.Paragraph type="secondary">
          {kind === 'refresh' && '立即重新收集硬體、軟體、健康與電池資料。'}
          {kind === 'lock' && '立即鎖定使用者的工作站。'}
          {kind === 'logoff' && '登出目前登入的使用者（未儲存的工作將遺失）。'}
        </Typography.Paragraph>
      )}
    </>
  )
}
