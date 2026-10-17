import { useState } from 'react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router'
import { Alert, Button, Card, Form, Modal, Select, Table, Typography } from 'antd'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { discoveryApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { DiscoveryScan, NewScan } from '../../api/types'
import { PageHeader } from '../../components/PageHeader'
import { ScanStatusTag } from '../../components/StatusTags'
import { DeviceSingleSelect } from '../../components/Selectors'
import { RoleGate } from '../../components/RoleGate'
import { durationBetween, fmtTime } from '../../utils/format'
import { showSuccess } from '../../utils/feedback'

const defaultPorts = [22, 80, 135, 139, 443, 445, 515, 631, 3389, 8080, 9100]
const cidrRe = /^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})(\/(\d|[12]\d|3[0-2]))?$/

function validCidr(s: string) {
  const m = cidrRe.exec(s.trim())
  if (!m) return false
  return [1, 2, 3, 4].every((i) => Number(m[i]) <= 255)
}

export default function Discovery() {
  const navigate = useNavigate()
  const [open, setOpen] = useState(false)
  const [page, setPage] = useState(1)
  const params = { page, page_size: 50 }
  const { data, isFetching, refetch } = useQuery({
    queryKey: [...qk.scans, params],
    queryFn: () => discoveryApi.scans(params),
    placeholderData: keepPreviousData,
    refetchInterval: (q) => (q.state.data?.items.some((s) => s.status === 'running') ? 5000 : false),
  })

  return (
    <>
      <PageHeader
        title="設備探索"
        subtitle="掃描網段以找出印表機、網路設備與未安裝 Agent 的電腦"
        extra={
          <>
            <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching}>
              重新整理
            </Button>
            <RoleGate min="operator">
              <Button type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>
                新增掃描
              </Button>
            </RoleGate>
          </>
        }
      />
      <Card>
        <Table<DiscoveryScan>
          rowKey="id"
          size="middle"
          dataSource={data?.items}
          loading={isFetching && !data}
          onRow={(r) => ({ onClick: () => void navigate(`/discovery/${r.id}`), style: { cursor: 'pointer' } })}
          pagination={{ current: page, pageSize: 50, total: data?.total ?? 0, onChange: setPage, hideOnSinglePage: true }}
          scroll={{ x: 900 }}
          columns={[
            { title: '開始時間', dataIndex: 'started_at', width: 160, render: (v: string, r) => <Link to={`/discovery/${r.id}`}>{fmtTime(v, true)}</Link> },
            { title: '網段', dataIndex: 'cidrs', render: (v: string[]) => <span className="mono">{v?.join(', ')}</span> },
            { title: '狀態', dataIndex: 'status', width: 100, render: (s: string) => <ScanStatusTag status={s} /> },
            { title: '發現主機', dataIndex: 'found', width: 100 },
            { title: '探索代理', dataIndex: 'probe_device_name', width: 140, render: (v: string | null) => v || '伺服器' },
            { title: '耗時', key: 'dur', width: 100, render: (_, r) => durationBetween(r.started_at, r.finished_at) },
            { title: '發起者', dataIndex: 'requested_by_name', width: 110 },
            {
              title: '錯誤',
              dataIndex: 'error',
              ellipsis: true,
              render: (v: string) => (v ? <Typography.Text type="danger">{v}</Typography.Text> : '-'),
            },
          ]}
        />
      </Card>
      <NewScanModal open={open} onClose={() => setOpen(false)} onCreated={(id) => void navigate(`/discovery/${id}`)} />
    </>
  )
}

function NewScanModal({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated: (id: string) => void }) {
  const [form] = Form.useForm<{ cidrs: string[]; ports: (string | number)[]; probe_device_id?: string }>()
  const qc = useQueryClient()
  const m = useMutation({
    mutationFn: (body: NewScan) => discoveryApi.create(body),
    onSuccess: (s) => {
      showSuccess('掃描已開始')
      void qc.invalidateQueries({ queryKey: qk.scans })
      onClose()
      onCreated(s.id)
    },
  })
  const submit = async () => {
    const v = await form.validateFields()
    const ports = (v.ports ?? []).map(Number).filter((p) => p > 0 && p < 65536)
    m.mutate({
      cidrs: v.cidrs.map((c) => c.trim()),
      ports: ports.length ? ports : undefined,
      probe_device_id: v.probe_device_id || undefined,
    })
  }
  return (
    <Modal title="新增網段掃描" open={open} onCancel={onClose} onOk={() => void submit()} okText="開始掃描" confirmLoading={m.isPending} width={600} destroyOnHidden>
      <Alert type="info" showIcon style={{ marginBottom: 16 }} title="掃描以 ICMP 與 TCP 常用埠探測，具速率限制，不會影響網路運作。" />
      <Form form={form} layout="vertical" preserve={false} initialValues={{ cidrs: [], ports: defaultPorts }}>
        <Form.Item
          label="網段（CIDR，可多個）"
          name="cidrs"
          rules={[
            { required: true, message: '請輸入至少一個網段' },
            {
              validator: (_, v?: string[]) => {
                const bad = (v ?? []).filter((c) => !validCidr(c))
                return bad.length ? Promise.reject(new Error(`格式錯誤：${bad.join(', ')}`)) : Promise.resolve()
              },
            },
          ]}
          extra="例如 10.0.1.0/24，輸入後按 Enter"
        >
          <Select mode="tags" tokenSeparators={[',', ' ', '，']} open={false} suffixIcon={null} placeholder="10.0.1.0/24" />
        </Form.Item>
        <Form.Item label="掃描埠" name="ports" extra="留空則使用伺服器預設埠">
          <Select
            mode="tags"
            tokenSeparators={[',', ' ']}
            options={defaultPorts.map((p) => ({ value: p, label: String(p) }))}
            placeholder="22, 80, 443…"
          />
        </Form.Item>
        <Form.Item label="探索代理裝置（選填）" name="probe_device_id" extra="伺服器無法直達的網段，可指定一台線上的 Agent 代為掃描。">
          <DeviceSingleSelect placeholder="搜尋線上裝置" filter={{ status: 'online', has_agent: 'true' }} />
        </Form.Item>
      </Form>
    </Modal>
  )
}
