import { useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Card, Flex, Input, Table, Tag, Typography } from 'antd'
import { deviceApi } from '../../../api/endpoints'
import { qk } from '../../../api/keys'
import { withKeys } from '../../../utils/table'
import type { InventoryChange, Software } from '../../../api/types'
import { useDebounced } from '../../../hooks/useDebounced'
import { fmtTime } from '../../../utils/format'

const changeLabels: Record<string, { text: string; color: string }> = {
  added: { text: '新增', color: 'green' },
  removed: { text: '移除', color: 'red' },
  changed: { text: '變更', color: 'blue' },
  updated: { text: '更新', color: 'blue' },
}

export default function SoftwareTab({ deviceId }: { deviceId: string }) {
  const [search, setSearch] = useState('')
  const q = useDebounced(search, 300)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const params = { q: q || undefined, page, page_size: pageSize }

  const { data, isFetching } = useQuery({
    queryKey: qk.deviceSub(deviceId, 'software', params),
    queryFn: () => deviceApi.software(deviceId, params),
    placeholderData: keepPreviousData,
  })
  const inv = useQuery({ queryKey: qk.deviceSub(deviceId, 'inventory'), queryFn: () => deviceApi.inventory(deviceId) })
  const changes = useQuery({
    queryKey: qk.deviceSub(deviceId, 'inventory-changes'),
    queryFn: () => deviceApi.inventoryChanges(deviceId),
  })

  return (
    <Flex vertical gap={16}>
      <Card
        title={`已安裝軟體（${data?.total ?? inv.data?.software_count ?? 0}）`}
        extra={<Typography.Text type="secondary">盤點時間：{fmtTime(inv.data?.software_collected_at)}</Typography.Text>}
      >
        <Input.Search
          allowClear
          placeholder="搜尋軟體名稱或發行者"
          value={search}
          onChange={(e) => {
            setSearch(e.target.value)
            setPage(1)
          }}
          style={{ maxWidth: 320, marginBottom: 12 }}
        />
        <Table<Software>
          size="small"
          rowKey="_k"
          dataSource={withKeys(data?.items)}
          loading={isFetching}
          scroll={{ x: 800 }}
          pagination={{
            current: page,
            pageSize,
            total: data?.total ?? 0,
            showSizeChanger: true,
            onChange: (p, s) => {
              setPage(p)
              setPageSize(s)
            },
            showTotal: (t) => `共 ${t} 項`,
          }}
          columns={[
            { title: '名稱', dataIndex: 'name', ellipsis: true },
            { title: '版本', dataIndex: 'version', width: 150, ellipsis: true },
            { title: '發行者', dataIndex: 'publisher', width: 220, ellipsis: true },
            { title: '安裝日期', dataIndex: 'install_date', width: 110, render: (v?: string) => v || '-' },
            { title: '架構', dataIndex: 'arch', width: 70, render: (v?: string) => v || '-' },
            {
              title: '範圍',
              dataIndex: 'scope',
              width: 90,
              render: (v?: string) => (v === 'user' ? <Tag>使用者</Tag> : v === 'machine' ? <Tag color="blue">電腦</Tag> : '-'),
            },
          ]}
        />
      </Card>
      <Card title="盤點變更紀錄">
        <Table<InventoryChange>
          size="small"
          rowKey="_k"
          dataSource={withKeys(changes.data)}
          loading={changes.isLoading}
          pagination={{ pageSize: 20, hideOnSinglePage: true }}
          scroll={{ x: 760 }}
          columns={[
            { title: '時間', dataIndex: 'at', width: 150, render: (v: string) => fmtTime(v) },
            { title: '類別', dataIndex: 'kind', width: 80, render: (v: string) => (v === 'hardware' ? '硬體' : v === 'software' ? '軟體' : v) },
            {
              title: '變更',
              dataIndex: 'change',
              width: 80,
              render: (v: string) => <Tag color={changeLabels[v]?.color}>{changeLabels[v]?.text ?? v}</Tag>,
            },
            { title: '項目', dataIndex: 'item', ellipsis: true },
            { title: '原值', dataIndex: 'old_value', ellipsis: true, render: (v: string) => v || '-' },
            { title: '新值', dataIndex: 'new_value', ellipsis: true, render: (v: string) => v || '-' },
          ]}
        />
      </Card>
    </Flex>
  )
}
