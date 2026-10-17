import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Button, Card, Col, Form, Input, Modal, Popconfirm, Progress, Row, Select, Space, Table, Tag, Tooltip, Typography, Upload } from 'antd'
import type { UploadFile } from 'antd'
import { DeleteOutlined, EditOutlined, InboxOutlined, PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { packageApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { Package, PackagePatch, PackageType } from '../../api/types'
import { PageHeader } from '../../components/PageHeader'
import { useAuth } from '../../hooks/useAuth'
import { fmtBytes, fmtTime } from '../../utils/format'
import { packageTypeLabels, toOptions } from '../../utils/labels'
import { showSuccess } from '../../utils/feedback'

const defaultArgs: Record<PackageType, string> = {
  msi: '/qn /norestart',
  exe: '/S',
  msix: '',
  ps1: '',
}

function parseCodes(s: string | undefined): number[] {
  return (s ?? '')
    .split(/[,，\s]+/)
    .map((x) => x.trim())
    .filter(Boolean)
    .map(Number)
    .filter((n) => Number.isInteger(n))
}

export default function Packages() {
  const { can } = useAuth()
  const qc = useQueryClient()
  const [uploadOpen, setUploadOpen] = useState(false)
  const [editing, setEditing] = useState<Package | null>(null)
  const { data, isFetching, refetch } = useQuery({ queryKey: qk.packages, queryFn: packageApi.list })
  const remove = useMutation({
    mutationFn: packageApi.remove,
    onSuccess: () => {
      showSuccess('已刪除套件')
      void qc.invalidateQueries({ queryKey: qk.packages })
    },
  })

  return (
    <>
      <PageHeader
        title="套件庫"
        subtitle="上傳 MSI／EXE／MSIX／PowerShell 套件以供部署"
        extra={
          <>
            <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching}>
              重新整理
            </Button>
            {can('operator') && (
              <Button type="primary" icon={<PlusOutlined />} onClick={() => setUploadOpen(true)}>
                上傳套件
              </Button>
            )}
          </>
        }
      />
      <Card>
        <Table<Package>
          rowKey="id"
          dataSource={data}
          loading={isFetching && !data}
          scroll={{ x: 1100 }}
          pagination={{ pageSize: 50, hideOnSinglePage: true }}
          columns={[
            {
              title: '名稱',
              dataIndex: 'name',
              render: (v: string, r) => (
                <div>
                  <Typography.Text strong>{v}</Typography.Text> <Typography.Text type="secondary">{r.version}</Typography.Text>
                  <div>
                    <Typography.Text type="secondary" style={{ fontSize: 12 }}>
                      {r.publisher}
                    </Typography.Text>
                  </div>
                </div>
              ),
            },
            { title: '類型', dataIndex: 'type', width: 90, render: (t: PackageType) => <Tag>{t.toUpperCase()}</Tag> },
            { title: '檔案', dataIndex: 'file_name', width: 180, ellipsis: true, render: (v: string, r) => <Tooltip title={v}>{v}（{fmtBytes(r.file_size)}）</Tooltip> },
            {
              title: 'SHA-256',
              dataIndex: 'sha256',
              width: 200,
              render: (v: string) => (
                <Typography.Text copyable={{ text: v }} className="mono" style={{ fontSize: 12 }}>
                  <Tooltip title={v}>{v ? `${v.slice(0, 16)}…` : '-'}</Tooltip>
                </Typography.Text>
              ),
            },
            { title: '安裝參數', dataIndex: 'install_args', width: 160, ellipsis: true, render: (v: string) => <span className="mono">{v || '-'}</span> },
            { title: '成功結束碼', dataIndex: 'success_codes', width: 120, render: (v: number[] | null) => v?.join(', ') || '0' },
            { title: '上傳者', dataIndex: 'created_by_name', width: 100 },
            { title: '上傳時間', dataIndex: 'created_at', width: 140, render: (v: string) => fmtTime(v) },
            {
              title: '操作',
              key: 'act',
              width: 110,
              fixed: 'right',
              render: (_, r) => (
                <Space size={0}>
                  {can('operator') && <Button type="link" size="small" icon={<EditOutlined />} onClick={() => setEditing(r)} aria-label="編輯" />}
                  {can('admin') && (
                    <Popconfirm title={`刪除「${r.name} ${r.version}」？`} okButtonProps={{ danger: true }} onConfirm={() => remove.mutateAsync(r.id)}>
                      <Button type="link" size="small" danger icon={<DeleteOutlined />} aria-label="刪除" />
                    </Popconfirm>
                  )}
                </Space>
              ),
            },
          ]}
        />
      </Card>
      <UploadModal open={uploadOpen} onClose={() => setUploadOpen(false)} />
      <EditModal pkg={editing} onClose={() => setEditing(null)} />
    </>
  )
}

interface UploadValues {
  name: string
  version: string
  publisher: string
  type: PackageType
  install_args: string
  uninstall_args: string
  success_codes: string
}

function UploadModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [form] = Form.useForm<UploadValues>()
  const qc = useQueryClient()
  const [file, setFile] = useState<File | null>(null)
  const [progress, setProgress] = useState<number | null>(null)
  const m = useMutation({
    mutationFn: (fd: FormData) => packageApi.upload(fd, setProgress),
    onSuccess: (p) => {
      showSuccess(`已上傳 ${p.name} ${p.version}`)
      void qc.invalidateQueries({ queryKey: qk.packages })
      close()
    },
    onSettled: () => setProgress(null),
  })

  const close = () => {
    setFile(null)
    setProgress(null)
    onClose()
  }

  const onPick = (f: File) => {
    setFile(f)
    const ext = f.name.split('.').pop()?.toLowerCase() as PackageType | undefined
    const type = ext && ext in defaultArgs ? ext : undefined
    const base = f.name.replace(/\.[^.]+$/, '')
    const vm = /(\d+(?:\.\d+)+)/.exec(base)
    form.setFieldsValue({
      name: form.getFieldValue('name') || base.replace(/[-_]?v?\d+(\.\d+)+.*$/, '').replace(/[-_]/g, ' ').trim() || base,
      version: form.getFieldValue('version') || vm?.[1] || '',
      ...(type ? { type, install_args: defaultArgs[type] } : {}),
    })
    return false
  }

  const submit = async () => {
    const v = await form.validateFields()
    if (!file) return
    const fd = new FormData()
    fd.append('file', file)
    fd.append('name', v.name)
    fd.append('version', v.version ?? '')
    fd.append('publisher', v.publisher ?? '')
    fd.append('type', v.type)
    fd.append('install_args', v.install_args ?? '')
    fd.append('uninstall_args', v.uninstall_args ?? '')
    fd.append('success_codes', parseCodes(v.success_codes).join(','))
    m.mutate(fd)
  }

  const fileList: UploadFile[] = file ? [{ uid: '1', name: file.name, size: file.size, status: 'done' }] : []

  return (
    <Modal
      title="上傳軟體套件"
      open={open}
      onCancel={close}
      onOk={() => void submit()}
      okText="上傳"
      okButtonProps={{ disabled: !file }}
      confirmLoading={m.isPending}
      width={640}
      destroyOnHidden
      mask={{ closable: false }}
    >
      <Upload.Dragger
        accept=".msi,.exe,.msix,.ps1"
        multiple={false}
        beforeUpload={onPick}
        fileList={fileList}
        onRemove={() => {
          setFile(null)
          return true
        }}
        style={{ marginBottom: 16 }}
      >
        <p className="ant-upload-drag-icon">
          <InboxOutlined />
        </p>
        <p className="ant-upload-text">點擊或拖曳檔案至此</p>
        <p className="ant-upload-hint">支援 MSI、EXE、MSIX、PS1；伺服器會計算 SHA-256，Agent 安裝前會驗證。</p>
      </Upload.Dragger>
      {progress !== null && <Progress percent={progress} style={{ marginBottom: 12 }} />}
      <Form form={form} layout="vertical" preserve={false} initialValues={{ type: 'msi', install_args: defaultArgs.msi, success_codes: '0, 3010, 1641' }}>
        <Row gutter={12}>
          <Col span={12}>
            <Form.Item label="名稱" name="name" rules={[{ required: true, message: '請輸入名稱' }]}>
              <Input placeholder="7-Zip" />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item label="版本" name="version">
              <Input placeholder="24.08" />
            </Form.Item>
          </Col>
          <Col span={6}>
            <Form.Item label="類型" name="type" rules={[{ required: true }]}>
              <Select options={toOptions(packageTypeLabels)} onChange={(t: PackageType) => form.setFieldValue('install_args', defaultArgs[t])} />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="發行者" name="publisher">
              <Input />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="成功結束碼" name="success_codes" extra="以逗號分隔；3010／1641 表示需重新開機">
              <Input />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="靜默安裝參數" name="install_args">
              <Input className="mono" />
            </Form.Item>
          </Col>
          <Col span={12}>
            <Form.Item label="解除安裝參數" name="uninstall_args">
              <Input className="mono" />
            </Form.Item>
          </Col>
        </Row>
      </Form>
    </Modal>
  )
}

function EditModal({ pkg, onClose }: { pkg: Package | null; onClose: () => void }) {
  const [form] = Form.useForm<Omit<PackagePatch, 'success_codes'> & { success_codes: string }>()
  const qc = useQueryClient()
  const m = useMutation({
    mutationFn: (body: PackagePatch) => packageApi.update(pkg!.id, body),
    onSuccess: () => {
      showSuccess('已更新套件')
      void qc.invalidateQueries({ queryKey: qk.packages })
      onClose()
    },
  })
  return (
    <Modal
      title="編輯套件"
      open={!!pkg}
      onCancel={onClose}
      confirmLoading={m.isPending}
      destroyOnHidden
      onOk={() => void form.validateFields().then((v) => m.mutate({ ...v, success_codes: parseCodes(v.success_codes) }))}
    >
      {pkg && (
        <Form
          form={form}
          layout="vertical"
          preserve={false}
          initialValues={{ ...pkg, success_codes: (pkg.success_codes ?? []).join(', ') }}
        >
          <Form.Item label="名稱" name="name" rules={[{ required: true }]}>
            <Input />
          </Form.Item>
          <Form.Item label="版本" name="version">
            <Input />
          </Form.Item>
          <Form.Item label="發行者" name="publisher">
            <Input />
          </Form.Item>
          <Form.Item label="靜默安裝參數" name="install_args">
            <Input className="mono" />
          </Form.Item>
          <Form.Item label="解除安裝參數" name="uninstall_args">
            <Input className="mono" />
          </Form.Item>
          <Form.Item label="成功結束碼" name="success_codes">
            <Input />
          </Form.Item>
        </Form>
      )}
    </Modal>
  )
}
