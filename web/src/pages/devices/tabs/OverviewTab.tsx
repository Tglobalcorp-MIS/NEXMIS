import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  Button,
  Card,
  Col,
  DatePicker,
  Descriptions,
  Empty,
  Flex,
  Form,
  Input,
  Modal,
  Progress,
  Row,
  Select,
  Space,
  Tag,
  Timeline,
  Typography,
} from 'antd'
import { EditOutlined, SwapOutlined } from '@ant-design/icons'
import dayjs, { type Dayjs } from 'dayjs'
import { deviceApi } from '../../../api/endpoints'
import { qk } from '../../../api/keys'
import type { DeviceDetail, DevicePatch, DeviceType, LifecycleState } from '../../../api/types'
import { ListMeta, SimpleList } from '../../../components/SimpleList'
import { BoolTag, LifecycleTag, SeverityTag } from '../../../components/StatusTags'
import { GroupSelect, TagsInput } from '../../../components/Selectors'
import { useAuth } from '../../../hooks/useAuth'
import { daysUntil, fmtDate, fmtDuration, fmtRelative, fmtTime, joinList, percentColor } from '../../../utils/format'
import {
  consentLabels,
  deviceTypeLabels,
  issueLabel,
  labelOf,
  lifecycleLabels,
  lifecycleOrder,
  sourceLabels,
  toOptions,
} from '../../../utils/labels'
import { showSuccess } from '../../../utils/feedback'

export default function OverviewTab({ device }: { device: DeviceDetail }) {
  return (
    <Row gutter={[16, 16]}>
      <Col xs={24} xl={16}>
        <BasicInfo device={device} />
      </Col>
      <Col xs={24} xl={8}>
        <Flex vertical gap={16}>
          <LiveStatus device={device} />
          <Issues device={device} />
        </Flex>
      </Col>
      <Col xs={24} xl={14}>
        <AssetInfo device={device} />
      </Col>
      <Col xs={24} xl={10}>
        <Lifecycle device={device} />
      </Col>
    </Row>
  )
}

function BasicInfo({ device: d }: { device: DeviceDetail }) {
  return (
    <Card title="基本資訊" style={{ height: '100%' }}>
      <Descriptions size="small" column={{ xs: 1, sm: 2, md: 2, lg: 2, xl: 2, xxl: 2 }} bordered>
        <Descriptions.Item label="主機名稱">{d.hostname || '-'}</Descriptions.Item>
        <Descriptions.Item label="網域">{d.domain || '-'}</Descriptions.Item>
        <Descriptions.Item label="廠牌／型號">{[d.manufacturer, d.model].filter(Boolean).join(' ') || '-'}</Descriptions.Item>
        <Descriptions.Item label="序號">
          <Typography.Text copyable={!!d.serial_number}>{d.serial_number || '-'}</Typography.Text>
        </Descriptions.Item>
        <Descriptions.Item label="作業系統" span="filled">
          {d.os_name ? `${d.os_name}（${d.os_version}）` : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="IP 位址">
          <span className="mono">{joinList(d.ip_addresses)}</span>
        </Descriptions.Item>
        <Descriptions.Item label="MAC 位址">
          <span className="mono">{joinList(d.mac_addresses)}</span>
        </Descriptions.Item>
        <Descriptions.Item label="登入使用者">{d.logged_in_user || '-'}</Descriptions.Item>
        <Descriptions.Item label="開機時間">{d.online ? fmtDuration(d.uptime_sec) : '-'}</Descriptions.Item>
        <Descriptions.Item label="Agent 版本">{d.agent_version || '-'}</Descriptions.Item>
        <Descriptions.Item label="使用者端助手">
          {d.has_agent ? <BoolTag value={d.helper_online} yes="執行中" no="未執行" /> : '-'}
        </Descriptions.Item>
        <Descriptions.Item label="待重新開機">{d.has_agent ? <BoolTag value={d.pending_reboot} invert /> : '-'}</Descriptions.Item>
        <Descriptions.Item label="遠端同意政策">{labelOf(consentLabels, d.consent_policy)}</Descriptions.Item>
        <Descriptions.Item label="資料來源">
          {d.sources?.map((s) => <Tag key={s}>{labelOf(sourceLabels, s)}</Tag>) ?? '-'}
        </Descriptions.Item>
        <Descriptions.Item label="最後上線">
          {fmtTime(d.last_seen_at)} {d.last_seen_at && <Typography.Text type="secondary">（{fmtRelative(d.last_seen_at)}）</Typography.Text>}
        </Descriptions.Item>
        <Descriptions.Item label="註冊時間">{fmtTime(d.enrolled_at)}</Descriptions.Item>
        <Descriptions.Item label="首次發現">{fmtTime(d.first_seen_at)}</Descriptions.Item>
        {d.ad_dn && (
          <>
            <Descriptions.Item label="AD 物件" span="filled">
              <Typography.Text style={{ fontSize: 12 }} className="mono">
                {d.ad_dn}
              </Typography.Text>
            </Descriptions.Item>
            <Descriptions.Item label="AD 狀態">
              <BoolTag value={d.ad_enabled} yes="已啟用" no="已停用" />
            </Descriptions.Item>
            <Descriptions.Item label="AD 最後登入">{fmtTime(d.ad_last_logon)}</Descriptions.Item>
          </>
        )}
      </Descriptions>
    </Card>
  )
}

function LiveStatus({ device: d }: { device: DeviceDetail }) {
  const gauge = (label: string, v: number | null | undefined) => (
    <Flex vertical align="center" gap={4}>
      <Progress
        type="dashboard"
        percent={v === null || v === undefined ? 0 : Math.round(v)}
        size={110}
        strokeColor={v === null || v === undefined ? undefined : percentColor(v)}
        format={(p) => (v === null || v === undefined || !d.online ? '-' : `${p}%`)}
      />
      <Typography.Text type="secondary">{label}</Typography.Text>
    </Flex>
  )
  return (
    <Card title="即時狀態" extra={d.online ? <Tag color="success">即時</Tag> : <Tag>離線</Tag>}>
      {d.has_agent ? (
        <Flex justify="space-around">
          {gauge('CPU', d.online ? d.cpu_percent : null)}
          {gauge('記憶體', d.online ? d.mem_percent : null)}
        </Flex>
      ) : (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="未安裝 Agent，無即時資料" />
      )}
    </Card>
  )
}

function Issues({ device: d }: { device: DeviceDetail }) {
  const issues = d.issues ?? []
  return (
    <Card title={`問題清單（${issues.length}）`}>
      <SimpleList
        items={issues}
        rowKey={(i, idx) => `${i.code}-${idx}`}
        empty="沒有偵測到問題"
        render={(i) => <ListMeta
              avatar={<SeverityTag severity={i.severity} />}
              title={issueLabel(i.code)}
              description={i.message}
            />}
      />
    </Card>
  )
}

interface AssetForm {
  name: string
  device_type: DeviceType
  asset_tag: string
  owner_name: string
  department: string
  location: string
  purchase_date: Dayjs | null
  warranty_end: Dayjs | null
  notes: string
  group_id: string | null
  tags: string[]
}

function AssetInfo({ device: d }: { device: DeviceDetail }) {
  const { can } = useAuth()
  const qc = useQueryClient()
  const [editing, setEditing] = useState(false)
  const [form] = Form.useForm<AssetForm>()

  const m = useMutation({
    mutationFn: (body: DevicePatch) => deviceApi.update(d.id, body),
    onSuccess: () => {
      showSuccess('資產資訊已更新')
      setEditing(false)
      void qc.invalidateQueries({ queryKey: qk.device(d.id) })
      void qc.invalidateQueries({ queryKey: qk.devices })
      void qc.invalidateQueries({ queryKey: qk.groups })
    },
  })

  const startEdit = () => {
    form.setFieldsValue({
      name: d.name,
      device_type: d.device_type,
      asset_tag: d.asset_tag,
      owner_name: d.owner_name,
      department: d.department,
      location: d.location,
      purchase_date: d.purchase_date ? dayjs(d.purchase_date) : null,
      warranty_end: d.warranty_end ? dayjs(d.warranty_end) : null,
      notes: d.notes,
      group_id: d.group_id,
      tags: d.tags ?? [],
    })
    setEditing(true)
  }

  const save = async () => {
    const v = await form.validateFields()
    m.mutate({
      ...v,
      group_id: v.group_id ?? null,
      purchase_date: v.purchase_date ? v.purchase_date.format('YYYY-MM-DD') : null,
      warranty_end: v.warranty_end ? v.warranty_end.format('YYYY-MM-DD') : null,
    })
  }

  const warrantyDays = daysUntil(d.warranty_end)

  return (
    <Card
      title="資產資訊"
      style={{ height: '100%' }}
      extra={
        can('operator') &&
        (editing ? (
          <Space>
            <Button onClick={() => setEditing(false)}>取消</Button>
            <Button type="primary" loading={m.isPending} onClick={() => void save()}>
              儲存
            </Button>
          </Space>
        ) : (
          <Button icon={<EditOutlined />} onClick={startEdit}>
            編輯
          </Button>
        ))
      }
    >
      {editing ? (
        <Form form={form} layout="vertical">
          <Row gutter={12}>
            <Col xs={24} sm={12}>
              <Form.Item label="顯示名稱" name="name" rules={[{ required: true, message: '請輸入名稱' }]}>
                <Input />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="類型" name="device_type">
                <Select options={toOptions(deviceTypeLabels)} />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="資產編號" name="asset_tag">
                <Input />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="保管人" name="owner_name">
                <Input />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="部門" name="department">
                <Input />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="地點" name="location">
                <Input />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="購買日期" name="purchase_date">
                <DatePicker style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="保固到期" name="warranty_end">
                <DatePicker style={{ width: '100%' }} />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="群組" name="group_id">
                <GroupSelect allowNone />
              </Form.Item>
            </Col>
            <Col xs={24} sm={12}>
              <Form.Item label="標籤" name="tags">
                <TagsInput />
              </Form.Item>
            </Col>
            <Col span={24}>
              <Form.Item label="備註" name="notes">
                <Input.TextArea rows={3} />
              </Form.Item>
            </Col>
          </Row>
        </Form>
      ) : (
        <Descriptions size="small" column={{ xs: 1, sm: 2, md: 2, lg: 2, xl: 2, xxl: 2 }} bordered>
          <Descriptions.Item label="資產編號">{d.asset_tag || '-'}</Descriptions.Item>
          <Descriptions.Item label="保管人">{d.owner_name || '-'}</Descriptions.Item>
          <Descriptions.Item label="部門">{d.department || '-'}</Descriptions.Item>
          <Descriptions.Item label="地點">{d.location || '-'}</Descriptions.Item>
          <Descriptions.Item label="購買日期">{fmtDate(d.purchase_date)}</Descriptions.Item>
          <Descriptions.Item label="保固到期">
            {fmtDate(d.warranty_end)}{' '}
            {warrantyDays !== null && warrantyDays <= 90 && (
              <Tag color={warrantyDays < 0 ? 'red' : 'orange'}>{warrantyDays < 0 ? '已過期' : `剩 ${warrantyDays} 天`}</Tag>
            )}
          </Descriptions.Item>
          <Descriptions.Item label="群組">{d.group_name || '-'}</Descriptions.Item>
          <Descriptions.Item label="標籤">{d.tags?.length ? d.tags.map((t) => <Tag key={t}>{t}</Tag>) : '-'}</Descriptions.Item>
          <Descriptions.Item label="備註" span="filled">
            <Typography.Paragraph style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{d.notes || '-'}</Typography.Paragraph>
          </Descriptions.Item>
        </Descriptions>
      )}
    </Card>
  )
}

function Lifecycle({ device: d }: { device: DeviceDetail }) {
  const { can } = useAuth()
  const qc = useQueryClient()
  const [open, setOpen] = useState(false)
  const [form] = Form.useForm<{ state: LifecycleState; reason: string }>()
  const { data: events, isLoading } = useQuery({
    queryKey: qk.deviceSub(d.id, 'lifecycle'),
    queryFn: () => deviceApi.lifecycle(d.id),
  })
  const m = useMutation({
    mutationFn: (v: { state: LifecycleState; reason: string }) => deviceApi.setLifecycle(d.id, v),
    onSuccess: () => {
      showSuccess('生命週期已變更')
      setOpen(false)
      void qc.invalidateQueries({ queryKey: qk.device(d.id) })
      void qc.invalidateQueries({ queryKey: qk.devices })
    },
  })

  const sorted = [...(events ?? [])].sort((a, b) => dayjs(b.at).valueOf() - dayjs(a.at).valueOf())

  return (
    <Card
      title={
        <Space>
          生命週期 <LifecycleTag state={d.lifecycle_state} />
        </Space>
      }
      style={{ height: '100%' }}
      extra={
        can('operator') && (
          <Button icon={<SwapOutlined />} onClick={() => setOpen(true)}>
            變更狀態
          </Button>
        )
      }
      loading={isLoading}
    >
      {sorted.length ? (
        <div style={{ maxHeight: 360, overflow: 'auto', paddingTop: 8 }} className="nx-scroll">
          <Timeline
            items={sorted.map((e) => ({
              color: e.to_state === 'retired' ? 'red' : e.to_state === 'repair' ? 'orange' : e.to_state === 'in_use' ? 'green' : 'blue',
              content: (
                <div>
                  <div>
                    {e.from_state ? <LifecycleTag state={e.from_state} /> : <Tag>新建</Tag>}→ <LifecycleTag state={e.to_state} />
                  </div>
                  {e.reason && <div style={{ marginTop: 4 }}>{e.reason}</div>}
                  <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                    {fmtTime(e.at)} · {e.actor_name || '系統'}
                  </Typography.Text>
                </div>
              ),
            }))}
          />
        </div>
      ) : (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚無變更紀錄" />
      )}
      <Modal
        title="變更生命週期"
        open={open}
        onCancel={() => setOpen(false)}
        confirmLoading={m.isPending}
        okText="變更"
        destroyOnHidden
        onOk={() => void form.validateFields().then((v) => m.mutate(v))}
      >
        <Form form={form} layout="vertical" preserve={false} initialValues={{ state: d.lifecycle_state }}>
          <Form.Item label="新狀態" name="state" rules={[{ required: true }]}>
            <Select options={lifecycleOrder.map((s) => ({ value: s, label: lifecycleLabels[s], disabled: s === d.lifecycle_state }))} />
          </Form.Item>
          <Form.Item label="原因" name="reason" rules={[{ required: true, message: '請填寫變更原因' }]}>
            <Input.TextArea rows={3} placeholder="例如：螢幕故障送修（RMA 單號 12345）" />
          </Form.Item>
        </Form>
      </Modal>
    </Card>
  )
}
