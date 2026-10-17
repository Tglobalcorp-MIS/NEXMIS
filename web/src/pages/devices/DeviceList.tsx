import { useState } from 'react'
import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router'
import {
  Button,
  Card,
  Col,
  Dropdown,
  Flex,
  Form,
  Input,
  Modal,
  Progress,
  Row,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd'
import type { MenuProps, TableProps } from 'antd'
import {
  CloudUploadOutlined,
  CodeOutlined,
  DownOutlined,
  FolderOutlined,
  PlusOutlined,
  PoweroffOutlined,
  ReloadOutlined,
  TagsOutlined,
} from '@ant-design/icons'
import { commandApi, deploymentApi, deviceApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { Device, DeviceListParams, DeviceType, NewCommand, NewDevice } from '../../api/types'
import { PageHeader } from '../../components/PageHeader'
import { HealthTag, LifecycleTag, OnlineStatus } from '../../components/StatusTags'
import { GroupSelect, PackageSelect, TagsInput } from '../../components/Selectors'
import { useGroups } from '../../hooks/useGroups'
import { RebootModal, RunCommandDrawer } from '../../components/CommandModals'
import { RoleGate } from '../../components/RoleGate'
import { emptyTarget } from '../../utils/target'
import { useUrlState } from '../../hooks/useUrlState'
import { useDebounced } from '../../hooks/useDebounced'
import { useAuth } from '../../hooks/useAuth'
import { fmtRelative, fmtTime } from '../../utils/format'
import { deviceTypeLabels, healthLabels, labelOf, lifecycleLabels, toOptions } from '../../utils/labels'
import { showSuccess } from '../../utils/feedback'

const filterDefaults = {
  q: undefined as string | undefined,
  status: undefined as string | undefined,
  type: undefined as string | undefined,
  health: undefined as string | undefined,
  lifecycle: undefined as string | undefined,
  group_id: undefined as string | undefined,
  tag: undefined as string | undefined,
  has_agent: undefined as string | undefined,
  sort: undefined as string | undefined,
  page: '1' as string | undefined,
  page_size: '50' as string | undefined,
}

type BulkAction = 'command' | 'reboot' | 'deploy' | 'tags' | 'group' | null

export default function DeviceList() {
  const qc = useQueryClient()
  const { can } = useAuth()
  const [f, setF] = useUrlState(filterDefaults)
  const [search, setSearch] = useState(f.q ?? '')
  const q = useDebounced(search, 350)
  const [selected, setSelected] = useState<string[]>([])
  const [bulk, setBulk] = useState<BulkAction>(null)
  const [addOpen, setAddOpen] = useState(false)
  const { data: groups } = useGroups()

  const params: DeviceListParams = {
    q: q || undefined,
    status: f.status as DeviceListParams['status'],
    type: f.type as DeviceListParams['type'],
    health: f.health as DeviceListParams['health'],
    lifecycle: f.lifecycle as DeviceListParams['lifecycle'],
    group_id: f.group_id,
    tag: f.tag,
    has_agent: f.has_agent as DeviceListParams['has_agent'],
    sort: f.sort,
    page: Number(f.page) || 1,
    page_size: Number(f.page_size) || 50,
  }
  const { data, isFetching, refetch } = useQuery({
    queryKey: qk.deviceList(params),
    queryFn: () => deviceApi.list(params),
    placeholderData: keepPreviousData,
  })

  // 搜尋字串變動時回到第一頁（在事件處理中處理，避免 effect 內 setState）
  const onSearch = (v: string) => {
    setSearch(v)
    setF({ q: v || undefined, page: '1' })
  }

  const filterSelect = (key: keyof typeof filterDefaults, placeholder: string, options: { value: string; label: string }[], width = 130) => (
    <Select
      allowClear
      placeholder={placeholder}
      value={f[key]}
      onChange={(v?: string) => setF({ [key]: v, page: '1' })}
      options={options}
      style={{ width }}
    />
  )

  const columns: TableProps<Device>['columns'] = [
    {
      title: '名稱',
      dataIndex: 'name',
      key: 'name',
      sorter: true,
      fixed: 'left',
      width: 200,
      render: (v: string, r) => (
        <Space size={8}>
          <OnlineStatus online={r.online} hasAgent={r.has_agent} lastSeen={r.last_seen_at} showText={false} />
          <div style={{ lineHeight: 1.3 }}>
            <Link to={`/devices/${r.id}`}>{v}</Link>
            {r.asset_tag && (
              <div>
                <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                  {r.asset_tag}
                </Typography.Text>
              </div>
            )}
          </div>
        </Space>
      ),
    },
    {
      title: '類型',
      dataIndex: 'device_type',
      width: 110,
      render: (t: DeviceType, r) => (
        <span>
          {labelOf(deviceTypeLabels, t)}
          {t === 'windows' && r.chassis_type === 'laptop' && <Typography.Text type="secondary">（筆電）</Typography.Text>}
        </span>
      ),
    },
    {
      title: '作業系統',
      dataIndex: 'os_name',
      width: 180,
      ellipsis: true,
      render: (v: string, r) => (v ? <Tooltip title={`${v} ${r.os_version}`}>{v}</Tooltip> : '-'),
    },
    { title: '使用者', dataIndex: 'logged_in_user', width: 140, ellipsis: true, render: (v: string) => v || '-' },
    {
      title: 'IP',
      dataIndex: 'ip_addresses',
      width: 130,
      render: (v: string[] | null) => (
        <Tooltip title={v && v.length > 1 ? v.join('\n') : undefined}>
          <span className="mono">{v?.[0] ?? '-'}</span>
          {v && v.length > 1 && <Typography.Text type="secondary"> +{v.length - 1}</Typography.Text>}
        </Tooltip>
      ),
    },
    {
      title: '健康',
      dataIndex: 'health_status',
      width: 100,
      render: (v: Device['health_status'], r) => (
        <Tooltip title={r.issue_count ? `${r.issue_count} 個問題` : undefined}>
          <span>
            <HealthTag status={v} />
          </span>
        </Tooltip>
      ),
    },
    {
      title: '電池',
      dataIndex: 'battery_health',
      width: 110,
      render: (v: number | null) =>
        v === null || v === undefined ? (
          <Typography.Text type="secondary">-</Typography.Text>
        ) : (
          <Progress percent={Math.round(v)} size="small" status={v < 60 ? 'exception' : 'normal'} />
        ),
    },
    {
      title: '群組／標籤',
      key: 'group',
      width: 200,
      render: (_, r) => (
        <Flex gap={4} wrap>
          {r.group_name && (
            <Tag icon={<FolderOutlined />} color="blue">
              {r.group_name}
            </Tag>
          )}
          {r.tags?.map((t) => (
            <Tag key={t} className="clickable" onClick={() => setF({ tag: t, page: '1' })}>
              {t}
            </Tag>
          ))}
          {!r.group_name && !r.tags?.length && <Typography.Text type="secondary">-</Typography.Text>}
        </Flex>
      ),
    },
    { title: '生命週期', dataIndex: 'lifecycle_state', width: 90, render: (v: Device['lifecycle_state']) => <LifecycleTag state={v} /> },
    {
      title: '最後上線',
      dataIndex: 'last_seen_at',
      key: 'last_seen',
      sorter: true,
      width: 130,
      render: (v: string | null, r) =>
        r.online ? <Typography.Text type="success">目前在線</Typography.Text> : <Tooltip title={fmtTime(v)}>{fmtRelative(v)}</Tooltip>,
    },
  ]

  const sortOrder = (key: string) => (f.sort === key ? 'ascend' : f.sort === `-${key}` ? 'descend' : null)
  columns.forEach((c) => {
    if (c && 'sorter' in c && c.sorter && c.key) c.sortOrder = sortOrder(String(c.key))
  })

  const onTableChange: TableProps<Device>['onChange'] = (pg, _filters, sorter) => {
    const s = Array.isArray(sorter) ? sorter[0] : sorter
    const sort = s?.order && s.columnKey ? (s.order === 'descend' ? `-${String(s.columnKey)}` : String(s.columnKey)) : undefined
    setF({ page: String(pg.current ?? 1), page_size: String(pg.pageSize ?? 50), sort })
  }

  const runBatch = async (command: NewCommand) => {
    const r = await commandApi.batch({ target: { ...emptyTarget(), device_ids: selected }, command })
    showSuccess(`已派送至 ${r.count} 台裝置`)
    void qc.invalidateQueries({ queryKey: qk.commands })
  }

  const bulkItems: MenuProps['items'] = [
    { key: 'command', icon: <CodeOutlined />, label: '執行指令', disabled: !can('operator') },
    { key: 'reboot', icon: <PoweroffOutlined />, label: '重新開機', disabled: !can('operator') },
    { key: 'deploy', icon: <CloudUploadOutlined />, label: '部署軟體', disabled: !can('operator') },
    { type: 'divider' },
    { key: 'tags', icon: <TagsOutlined />, label: '加標籤', disabled: !can('operator') },
    { key: 'group', icon: <FolderOutlined />, label: '變更群組', disabled: !can('operator') },
  ]

  const hasFilter = !!(f.q || f.status || f.type || f.health || f.lifecycle || f.group_id || f.tag || f.has_agent)

  return (
    <>
      <PageHeader
        title="裝置清單"
        subtitle={data ? `共 ${data.total} 台` : undefined}
        extra={
          <>
            <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching}>
              重新整理
            </Button>
            <RoleGate min="operator">
              <Button type="primary" icon={<PlusOutlined />} onClick={() => setAddOpen(true)}>
                新增設備
              </Button>
            </RoleGate>
          </>
        }
      />
      <Card styles={{ body: { padding: 16 } }}>
        <Flex gap={8} wrap style={{ marginBottom: 12 }}>
          <Input.Search
            allowClear
            placeholder="搜尋名稱、IP、序號、使用者"
            value={search}
            onChange={(e) => onSearch(e.target.value)}
            style={{ width: 260 }}
          />
          {filterSelect('status', '狀態', [
            { value: 'online', label: '線上' },
            { value: 'offline', label: '離線' },
          ], 100)}
          {filterSelect('type', '類型', toOptions(deviceTypeLabels))}
          {filterSelect('health', '健康', toOptions(healthLabels), 100)}
          {filterSelect('lifecycle', '生命週期', toOptions(lifecycleLabels), 110)}
          {filterSelect('group_id', '群組', (groups ?? []).map((g) => ({ value: g.id, label: g.name })), 140)}
          {filterSelect('has_agent', 'Agent', [
            { value: 'true', label: '已安裝' },
            { value: 'false', label: '未安裝' },
          ], 110)}
          {f.tag && (
            <Tag closable onClose={() => setF({ tag: undefined, page: '1' })} color="processing" style={{ alignSelf: 'center' }}>
              標籤：{f.tag}
            </Tag>
          )}
          {hasFilter && (
            <Button
              type="link"
              onClick={() => {
                setSearch('')
                setF({ q: null, status: null, type: null, health: null, lifecycle: null, group_id: null, tag: null, has_agent: null, page: '1' })
              }}
            >
              清除篩選
            </Button>
          )}
        </Flex>

        {selected.length > 0 && (
          <Flex align="center" gap={12} style={{ marginBottom: 12 }}>
            <Typography.Text>已選取 {selected.length} 台</Typography.Text>
            <Dropdown menu={{ items: bulkItems, onClick: ({ key }) => setBulk(key as BulkAction) }}>
              <Button type="primary">
                批次操作 <DownOutlined />
              </Button>
            </Dropdown>
            <Button type="link" onClick={() => setSelected([])}>
              取消選取
            </Button>
          </Flex>
        )}

        <Table<Device>
          rowKey="id"
          size="middle"
          columns={columns}
          dataSource={data?.items}
          loading={isFetching && !data}
          onChange={onTableChange}
          scroll={{ x: 1400 }}
          rowSelection={{
            selectedRowKeys: selected,
            onChange: (keys) => setSelected(keys as string[]),
            preserveSelectedRowKeys: true,
          }}
          pagination={{
            current: params.page,
            pageSize: params.page_size,
            total: data?.total ?? 0,
            showSizeChanger: true,
            pageSizeOptions: [20, 50, 100, 200],
            showTotal: (t, r) => `第 ${r[0]}-${r[1]} 台，共 ${t} 台`,
          }}
        />
      </Card>

      <RunCommandDrawer
        open={bulk === 'command'}
        onClose={() => setBulk(null)}
        title={`批次執行指令（${selected.length} 台）`}
        onSubmit={runBatch}
      />
      <RebootModal open={bulk === 'reboot'} onClose={() => setBulk(null)} onSubmit={runBatch} description={<p>將對已選取的 {selected.length} 台裝置發出重新開機指令。</p>} />
      <BulkDeployModal open={bulk === 'deploy'} deviceIds={selected} onClose={() => setBulk(null)} />
      <BulkTagsModal open={bulk === 'tags'} devices={selected} onClose={() => setBulk(null)} />
      <BulkGroupModal open={bulk === 'group'} devices={selected} onClose={() => setBulk(null)} />
      <AddDeviceModal open={addOpen} onClose={() => setAddOpen(false)} />
    </>
  )
}

function BulkDeployModal({ open, deviceIds, onClose }: { open: boolean; deviceIds: string[]; onClose: () => void }) {
  const [form] = Form.useForm<{ name: string; package_id: string }>()
  const qc = useQueryClient()
  const m = useMutation({
    mutationFn: (v: { name: string; package_id: string }) =>
      deploymentApi.create({ name: v.name, package_id: v.package_id, target: { ...emptyTarget(), device_ids: deviceIds } }),
    onSuccess: () => {
      showSuccess('已建立部署工作')
      void qc.invalidateQueries({ queryKey: qk.deployments })
      onClose()
    },
  })
  return (
    <Modal title={`部署軟體（${deviceIds.length} 台）`} open={open} onCancel={onClose} confirmLoading={m.isPending} onOk={() => void form.validateFields().then((v) => m.mutate(v))} okText="建立部署" destroyOnHidden>
      <Form form={form} layout="vertical" preserve={false}>
        <Form.Item label="套件" name="package_id" rules={[{ required: true, message: '請選擇套件' }]}>
          <PackageSelect />
        </Form.Item>
        <Form.Item label="部署名稱" name="name" rules={[{ required: true, message: '請輸入名稱' }]}>
          <Input placeholder="例如：部署 7-Zip 至財務部" />
        </Form.Item>
      </Form>
    </Modal>
  )
}

function BulkTagsModal({ open, devices, onClose }: { open: boolean; devices: string[]; onClose: () => void }) {
  const [tags, setTags] = useState<string[]>([])
  const qc = useQueryClient()
  const m = useMutation({
    mutationFn: () => deviceApi.bulk({ device_ids: devices, add_tags: tags }),
    onSuccess: (r) => {
      showSuccess(`已為 ${r.updated} 台裝置加上標籤`)
      void qc.invalidateQueries({ queryKey: qk.devices })
      setTags([])
      onClose()
    },
  })
  return (
    <Modal title={`加標籤（${devices.length} 台）`} open={open} onCancel={onClose} onOk={() => m.mutate()} okButtonProps={{ disabled: !tags.length }} confirmLoading={m.isPending} okText="加入" destroyOnHidden>
      <Typography.Paragraph type="secondary">標籤會加入至既有標籤，不會移除原有標籤。</Typography.Paragraph>
      <TagsInput value={tags} onChange={setTags} />
    </Modal>
  )
}

function BulkGroupModal({ open, devices, onClose }: { open: boolean; devices: string[]; onClose: () => void }) {
  const [group, setGroup] = useState<string | null | undefined>(undefined)
  const qc = useQueryClient()
  const m = useMutation({
    mutationFn: () => deviceApi.bulk({ device_ids: devices, group_id: group ?? '' }),
    onSuccess: (r) => {
      showSuccess(`已更新 ${r.updated} 台裝置的群組`)
      void qc.invalidateQueries({ queryKey: qk.devices })
      void qc.invalidateQueries({ queryKey: qk.groups })
      onClose()
    },
  })
  return (
    <Modal title={`變更群組（${devices.length} 台）`} open={open} onCancel={onClose} onOk={() => m.mutate()} okButtonProps={{ disabled: group === undefined }} confirmLoading={m.isPending} destroyOnHidden>
      <GroupSelect allowNone value={group} onChange={(v) => setGroup(v as string | null)} placeholder="選擇群組（或無群組）" />
    </Modal>
  )
}

function AddDeviceModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [form] = Form.useForm<NewDevice>()
  const qc = useQueryClient()
  const m = useMutation({
    mutationFn: deviceApi.create,
    onSuccess: () => {
      showSuccess('已新增設備')
      void qc.invalidateQueries({ queryKey: qk.devices })
      onClose()
    },
  })
  return (
    <Modal
      title="新增非 Agent 設備"
      open={open}
      onCancel={onClose}
      width={640}
      confirmLoading={m.isPending}
      okText="新增"
      destroyOnHidden
      onOk={() =>
        void form.validateFields().then((v) =>
          m.mutate({ ...v, ip_addresses: v.ip_addresses ?? [], mac_addresses: v.mac_addresses ?? [], group_id: v.group_id ?? null }),
        )
      }
    >
      <Typography.Paragraph type="secondary">印表機、網路設備、NAS 等無法安裝 Agent 的設備可在此手動登錄。</Typography.Paragraph>
      <Form form={form} layout="vertical" preserve={false} initialValues={{ device_type: 'printer' }}>
        <Row gutter={12}>
          <Col span={14}>
            <Form.Item label="名稱" name="name" rules={[{ required: true, message: '請輸入名稱' }]}>
              <Input placeholder="例如 PRN-5F-01" />
            </Form.Item>
          </Col>
          <Col span={10}>
            <Form.Item label="類型" name="device_type" rules={[{ required: true }]}>
              <Select options={toOptions(deviceTypeLabels)} />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item
              label="IP 位址"
              name="ip_addresses"
              rules={[
                {
                  validator: (_, v?: string[]) =>
                    !v || v.every((x) => /^(\d{1,3}\.){3}\d{1,3}$|:/.test(x)) ? Promise.resolve() : Promise.reject(new Error('IP 格式錯誤')),
                },
              ]}
            >
              <TagsInput placeholder="輸入後按 Enter" />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="MAC 位址" name="mac_addresses">
              <TagsInput placeholder="00:11:22:33:44:55" />
            </Form.Item>
          </Col>
          <Col span={8}>
            <Form.Item label="廠牌" name="manufacturer">
              <Input />
            </Form.Item>
          </Col>
          <Col span={8}>
            <Form.Item label="型號" name="model">
              <Input />
            </Form.Item>
          </Col>
          <Col span={8}>
            <Form.Item label="序號" name="serial_number">
              <Input />
            </Form.Item>
          </Col>
          <Col span={8}>
            <Form.Item label="資產編號" name="asset_tag">
              <Input />
            </Form.Item>
          </Col>
          <Col span={8}>
            <Form.Item label="地點" name="location">
              <Input />
            </Form.Item>
          </Col>
          <Col span={8}>
            <Form.Item label="部門" name="department">
              <Input />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="群組" name="group_id">
              <GroupSelect />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="標籤" name="tags">
              <TagsInput />
            </Form.Item>
          </Col>
          <Col span={24}>
            <Form.Item label="備註" name="notes">
              <Input.TextArea rows={2} />
            </Form.Item>
          </Col>
        </Row>
      </Form>
    </Modal>
  )
}
