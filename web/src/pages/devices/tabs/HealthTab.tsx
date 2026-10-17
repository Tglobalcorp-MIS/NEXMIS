import { useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { Card, Col, Descriptions, Empty, Flex, Row, Segmented, Skeleton, Table, Tag, Typography } from 'antd'
import { deviceApi } from '../../../api/endpoints'
import { qk } from '../../../api/keys'
import { withKeys } from '../../../utils/table'
import type { EventSample, ProcessInfo } from '../../../api/types'
import { ListMeta, SimpleList } from '../../../components/SimpleList'
import { BoolTag, SeverityTag } from '../../../components/StatusTags'
import { TimeLineChart } from '../../../components/charts'
import { DiskHealthTag, VolumeBars } from '../../../components/HardwareBits'
import { fmtBytes, fmtDuration, fmtTime } from '../../../utils/format'
import { issueLabel } from '../../../utils/labels'

const rebootReasonLabels: Record<string, string> = {
  CBS: '元件服務（CBS）',
  WindowsUpdate: 'Windows Update',
  PendingFileRename: '待處理的檔案重新命名',
  ComputerRename: '電腦重新命名',
  SCCM: 'SCCM 用戶端',
}

export default function HealthTab({ deviceId }: { deviceId: string }) {
  const [days, setDays] = useState(7)
  const { data, isLoading } = useQuery({
    queryKey: qk.deviceSub(deviceId, 'health', days),
    queryFn: () => deviceApi.health(deviceId, days),
  })
  if (isLoading) return <Skeleton active paragraph={{ rows: 10 }} />
  const h = data?.latest
  const history = data?.history ?? []

  return (
    <Row gutter={[16, 16]}>
      <Col span={24}>
        <Card
          title="CPU／記憶體歷史"
          extra={
            <Segmented
              value={days}
              onChange={(v) => setDays(v)}
              options={[
                { label: '1 天', value: 1 },
                { label: '7 天', value: 7 },
                { label: '30 天', value: 30 },
              ]}
            />
          }
        >
          {history.length ? (
            <TimeLineChart
              data={history}
              xKey="collected_at"
              yDomain={[0, 100]}
              series={[
                { key: 'cpu_percent', name: 'CPU' },
                { key: 'mem_percent', name: '記憶體' },
              ]}
              timeFormat={days <= 1 ? 'HH:mm' : 'MM-DD HH:mm'}
            />
          ) : (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="此期間沒有資料" />
          )}
        </Card>
      </Col>
      {!h ? (
        <Col span={24}>
          <Empty description="尚未收到健康診斷資料" />
        </Col>
      ) : (
        <>
          <Col span={24}>
            <Typography.Text type="secondary">最新診斷時間：{fmtTime(h.collected)}</Typography.Text>
          </Col>
          <Col xs={24} xl={12}>
            <Card title="安全與系統" size="small" style={{ height: '100%' }}>
              <Descriptions size="small" column={1} bordered>
                <Descriptions.Item label="Defender 防毒">
                  <BoolTag value={h.defender_enabled} yes="已啟用" no="未啟用" />
                </Descriptions.Item>
                <Descriptions.Item label="即時保護">
                  <BoolTag value={h.defender_realtime} yes="已開啟" no="已關閉" />
                </Descriptions.Item>
                <Descriptions.Item label="病毒碼">
                  {h.defender_sig_age_days === null || h.defender_sig_age_days === undefined ? (
                    '-'
                  ) : (
                    <Tag color={h.defender_sig_age_days > 3 ? 'warning' : 'success'}>{h.defender_sig_age_days} 天前更新</Tag>
                  )}
                </Descriptions.Item>
                <Descriptions.Item label="防火牆">
                  <BoolTag value={h.firewall_enabled} yes="已啟用" no="未啟用" />
                </Descriptions.Item>
                <Descriptions.Item label="最近安裝更新">{fmtTime(h.last_update_install)}</Descriptions.Item>
                <Descriptions.Item label="上次開機">
                  {fmtTime(h.last_boot)}（已執行 {fmtDuration(h.uptime_sec)}）
                </Descriptions.Item>
                <Descriptions.Item label="待重新開機">
                  <BoolTag value={h.pending_reboot} invert />
                  {h.pending_reboot_reasons?.map((r) => (
                    <Tag key={r} style={{ marginTop: 4 }}>
                      {rebootReasonLabels[r] ?? r}
                    </Tag>
                  ))}
                </Descriptions.Item>
                <Descriptions.Item label="24 小時事件錯誤">
                  <Tag color={h.event_errors_24h > 50 ? 'error' : h.event_errors_24h > 10 ? 'warning' : 'default'}>{h.event_errors_24h} 筆</Tag>
                </Descriptions.Item>
              </Descriptions>
            </Card>
          </Col>
          <Col xs={24} xl={12}>
            <Card title="診斷問題" size="small" style={{ height: '100%' }}>
              <SimpleList
        items={h.issues ?? []}
        rowKey={(i, idx) => `${i.code}-${idx}`}
        empty="沒有偵測到問題"
        render={(i) => <ListMeta avatar={<SeverityTag severity={i.severity} />} title={issueLabel(i.code)} description={i.message} />}
      />
              {!!h.stopped_auto_services?.length && (
                <>
                  <Typography.Title level={5} style={{ marginTop: 16 }}>
                    已停止的自動啟動服務
                  </Typography.Title>
                  <Flex gap={4} wrap>
                    {h.stopped_auto_services.map((s) => (
                      <Tag key={s} color="orange">
                        {s}
                      </Tag>
                    ))}
                  </Flex>
                </>
              )}
            </Card>
          </Col>
          <Col xs={24} xl={12}>
            <Card title="磁碟區" size="small">
              <VolumeBars volumes={h.volumes ?? []} />
              {!!h.disk_health?.length && (
                <Flex gap={8} wrap style={{ marginTop: 12 }}>
                  {h.disk_health.map((d, i) => (
                    <span key={`${d.serial_number}-${i}`}>
                      {d.model} <DiskHealthTag health={d.health} />
                    </span>
                  ))}
                </Flex>
              )}
            </Card>
          </Col>
          <Col xs={24} xl={12}>
            <Card title="資源使用前幾名的處理程序" size="small">
              <Table<ProcessInfo>
                size="small"
                pagination={false}
                rowKey="_k"
                dataSource={withKeys(h.top_processes)}
                columns={[
                  { title: '名稱', dataIndex: 'name', ellipsis: true },
                  { title: 'PID', dataIndex: 'pid', width: 80 },
                  { title: 'CPU', dataIndex: 'cpu', width: 80, render: (v: number) => `${v.toFixed(1)}%` },
                  { title: '記憶體', dataIndex: 'mem_bytes', width: 100, render: (v: number) => fmtBytes(v) },
                ]}
              />
            </Card>
          </Col>
          <Col span={24}>
            <Card title="事件記錄錯誤（24 小時）" size="small">
              <Table<EventSample>
                size="small"
                pagination={false}
                rowKey="_k"
                dataSource={withKeys(h.top_events)}
                scroll={{ x: 800 }}
                columns={[
                  { title: '記錄', dataIndex: 'log', width: 100 },
                  { title: '來源', dataIndex: 'source', width: 180, ellipsis: true },
                  { title: '事件 ID', dataIndex: 'event_id', width: 80 },
                  { title: '次數', dataIndex: 'count', width: 70 },
                  { title: '最後發生', dataIndex: 'last_time', width: 150, render: (v: string) => fmtTime(v) },
                  { title: '訊息', dataIndex: 'message', ellipsis: true },
                ]}
              />
            </Card>
          </Col>
        </>
      )}
    </Row>
  )
}
