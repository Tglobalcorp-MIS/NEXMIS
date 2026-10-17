import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router'
import { Alert, Button, Card, Descriptions, Flex, Input, Result, Skeleton, Table, Tag, Typography } from 'antd'
import { ArrowLeftOutlined, ImportOutlined } from '@ant-design/icons'
import { discoveryApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import type { DiscoveredHost } from '../../api/types'
import { PageHeader } from '../../components/PageHeader'
import { ScanStatusTag } from '../../components/StatusTags'
import { useAuth } from '../../hooks/useAuth'
import { durationBetween, fmtTime } from '../../utils/format'
import { deviceTypeLabels, labelOf } from '../../utils/labels'
import { showSuccess } from '../../utils/feedback'

const ipNum = (ip: string) => ip.split('.').reduce((a, b) => a * 256 + (Number(b) || 0), 0)

export default function ScanDetail() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { can } = useAuth()
  const [selected, setSelected] = useState<string[]>([])
  const [search, setSearch] = useState('')

  const { data, isLoading, error } = useQuery({
    queryKey: qk.scan(id),
    queryFn: () => discoveryApi.scan(id),
    refetchInterval: (q) => (q.state.data?.status === 'running' ? 3000 : false),
  })
  const imp = useMutation({
    mutationFn: () => discoveryApi.importHosts({ scan_id: id, ips: selected }),
    onSuccess: () => {
      showSuccess(`已匯入 ${selected.length} 台設備`)
      setSelected([])
      void qc.invalidateQueries({ queryKey: qk.scan(id) })
      void qc.invalidateQueries({ queryKey: qk.devices })
    },
  })

  if (isLoading) return <Skeleton active />
  if (error || !data) return <Result status="error" title="無法載入掃描結果" subTitle={error?.message} />

  const q = search.trim().toLowerCase()
  const hosts = (data.hosts ?? [])
    .filter((h) => !q || [h.ip, h.hostname, h.vendor, h.mac].some((x) => x?.toLowerCase().includes(q)))
    .sort((a, b) => ipNum(a.ip) - ipNum(b.ip))

  return (
    <>
      <PageHeader
        back={<Button type="text" icon={<ArrowLeftOutlined />} onClick={() => void navigate('/discovery')} aria-label="返回" />}
        title="掃描結果"
        subtitle={<span className="mono">{data.cidrs?.join(', ')}</span>}
        extra={
          can('operator') && (
            <Button type="primary" icon={<ImportOutlined />} disabled={!selected.length} loading={imp.isPending} onClick={() => imp.mutate()}>
              匯入為裝置（{selected.length}）
            </Button>
          )
        }
      />
      <Card style={{ marginBottom: 16 }}>
        <Descriptions size="small" column={{ xs: 1, sm: 2, md: 2, lg: 4, xl: 4, xxl: 4 }}>
          <Descriptions.Item label="狀態">
            <ScanStatusTag status={data.status} />
          </Descriptions.Item>
          <Descriptions.Item label="發現主機">{data.found}</Descriptions.Item>
          <Descriptions.Item label="探索代理">{data.probe_device_name || '伺服器'}</Descriptions.Item>
          <Descriptions.Item label="發起者">{data.requested_by_name}</Descriptions.Item>
          <Descriptions.Item label="開始">{fmtTime(data.started_at, true)}</Descriptions.Item>
          <Descriptions.Item label="完成">{fmtTime(data.finished_at, true)}</Descriptions.Item>
          <Descriptions.Item label="耗時">{durationBetween(data.started_at, data.finished_at)}</Descriptions.Item>
        </Descriptions>
        {data.error && <Alert type="error" showIcon title={data.error} style={{ marginTop: 12 }} />}
      </Card>
      <Card>
        <Flex justify="space-between" wrap gap={8} style={{ marginBottom: 12 }}>
          <Input.Search allowClear placeholder="搜尋 IP、主機名稱、廠商、MAC" value={search} onChange={(e) => setSearch(e.target.value)} style={{ width: 300 }} />
          <Typography.Text type="secondary">已登錄的主機無法再次匯入</Typography.Text>
        </Flex>
        <Table<DiscoveredHost>
          rowKey="ip"
          size="small"
          dataSource={hosts}
          loading={data.status === 'running' && !hosts.length}
          pagination={{ pageSize: 100, hideOnSinglePage: true, showTotal: (t) => `共 ${t} 台` }}
          scroll={{ x: 900 }}
          rowSelection={
            can('operator')
              ? {
                  selectedRowKeys: selected,
                  onChange: (k) => setSelected(k as string[]),
                  getCheckboxProps: (h) => ({ disabled: !!h.device_id }),
                }
              : undefined
          }
          columns={[
            { title: 'IP', dataIndex: 'ip', width: 130, render: (v: string) => <span className="mono">{v}</span> },
            { title: '主機名稱', dataIndex: 'hostname', render: (v: string) => v || '-' },
            { title: 'MAC', dataIndex: 'mac', width: 160, render: (v: string) => <span className="mono">{v || '-'}</span> },
            { title: '廠商', dataIndex: 'vendor', width: 140, render: (v: string) => v || '-' },
            { title: '推測類型', dataIndex: 'device_type', width: 110, render: (v: string) => labelOf(deviceTypeLabels, v || 'unknown') },
            {
              title: '開放埠',
              dataIndex: 'open_ports',
              render: (v: number[] | null) => (
                <Flex gap={2} wrap>
                  {(v ?? []).map((p) => (
                    <Tag key={p} style={{ marginInlineEnd: 0 }}>
                      {p}
                    </Tag>
                  ))}
                </Flex>
              ),
            },
            { title: '延遲', dataIndex: 'rtt_ms', width: 80, render: (v: number) => (v ? `${v} ms` : '-') },
            {
              title: '已登錄',
              dataIndex: 'device_id',
              width: 90,
              render: (v: string | null) => (v ? <Link to={`/devices/${v}`}>查看</Link> : <Typography.Text type="secondary">否</Typography.Text>),
            },
          ]}
        />
      </Card>
    </>
  )
}
