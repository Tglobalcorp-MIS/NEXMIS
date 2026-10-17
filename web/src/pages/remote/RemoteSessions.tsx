import { useState } from 'react'
import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Button, Card, Flex, Select } from 'antd'
import { ReloadOutlined } from '@ant-design/icons'
import { remoteApi } from '../../api/endpoints'
import { qk } from '../../api/keys'
import { PageHeader } from '../../components/PageHeader'
import { RemoteSessionTable } from '../../components/RemoteSessionTable'
import { remoteStatusLabels, toOptions } from '../../utils/labels'

export default function RemoteSessions() {
  const [status, setStatus] = useState<string | undefined>()
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(50)
  const params = { status, page, page_size: pageSize }
  const { data, isFetching, refetch } = useQuery({
    queryKey: [...qk.remoteSessions, params],
    queryFn: () => remoteApi.list(params),
    placeholderData: keepPreviousData,
  })
  return (
    <>
      <PageHeader
        title="遠端連線紀錄"
        subtitle="所有遠端檢視／操作的請求與連線起訖"
        extra={
          <Button icon={<ReloadOutlined />} onClick={() => void refetch()} loading={isFetching}>
            重新整理
          </Button>
        }
      />
      <Card>
        <Flex gap={8} style={{ marginBottom: 12 }}>
          <Select
            allowClear
            placeholder="狀態"
            value={status}
            onChange={(v?: string) => {
              setStatus(v)
              setPage(1)
            }}
            options={toOptions(remoteStatusLabels)}
            style={{ width: 140 }}
          />
        </Flex>
        <RemoteSessionTable
          data={status ? data?.items.filter((s) => s.status === status) : data?.items}
          loading={isFetching && !data}
          pagination={{
            current: page,
            pageSize,
            total: data?.total ?? 0,
            showSizeChanger: true,
            onChange: (p, s) => {
              setPage(p)
              setPageSize(s)
            },
            showTotal: (t) => `共 ${t} 筆`,
          }}
        />
      </Card>
    </>
  )
}
