import { useQuery } from '@tanstack/react-query'
import { Card, Col, Descriptions, Empty, Flex, Row, Skeleton, Table, Tag, Typography } from 'antd'
import { deviceApi } from '../../../api/endpoints'
import { qk } from '../../../api/keys'
import { withKeys } from '../../../utils/table'
import type { Disk, GPU, MemModule, Monitor, NIC } from '../../../api/types'
import { BoolTag } from '../../../components/StatusTags'
import { DiskHealthTag, VolumeBars } from '../../../components/HardwareBits'
import { fmtBytes, fmtMhz, fmtSpeed, fmtTime, joinList } from '../../../utils/format'

export default function HardwareTab({ deviceId }: { deviceId: string }) {
  const { data, isLoading } = useQuery({
    queryKey: qk.deviceSub(deviceId, 'inventory'),
    queryFn: () => deviceApi.inventory(deviceId),
  })
  if (isLoading) return <Skeleton active paragraph={{ rows: 10 }} />
  const hw = data?.hardware
  if (!hw) return <Empty description="尚未收到硬體盤點資料" />

  const cpus = hw.cpus ?? []
  return (
    <Row gutter={[16, 16]}>
      <Col span={24}>
        <Typography.Text type="secondary">盤點時間：{fmtTime(data?.hardware_collected_at)}</Typography.Text>
      </Col>
      <Col xs={24} xl={12}>
        <Card title="系統" size="small">
          <Descriptions size="small" column={1} bordered>
            <Descriptions.Item label="廠牌／型號">{`${hw.manufacturer} ${hw.model}`.trim() || '-'}</Descriptions.Item>
            <Descriptions.Item label="序號">{hw.serial_number || '-'}</Descriptions.Item>
            <Descriptions.Item label="機箱類型">{hw.chassis_type || '-'}</Descriptions.Item>
            <Descriptions.Item label="作業系統">{`${hw.os_name} ${hw.os_edition ? `(${hw.os_edition})` : ''}`}</Descriptions.Item>
            <Descriptions.Item label="版本／組建">{`${hw.os_version} / ${hw.os_build}`}</Descriptions.Item>
            <Descriptions.Item label="架構">{hw.os_arch || '-'}</Descriptions.Item>
            <Descriptions.Item label="安裝日期">{hw.os_install_date || '-'}</Descriptions.Item>
            <Descriptions.Item label="網域">
              {hw.domain || '-'} {hw.domain_joined ? <Tag color="blue">已加入網域</Tag> : <Tag>工作群組</Tag>}
            </Descriptions.Item>
          </Descriptions>
        </Card>
      </Col>
      <Col xs={24} xl={12}>
        <Card title="BIOS 與安全性" size="small">
          <Descriptions size="small" column={1} bordered>
            <Descriptions.Item label="BIOS 廠商">{hw.bios_vendor || '-'}</Descriptions.Item>
            <Descriptions.Item label="BIOS 版本">{hw.bios_version || '-'}</Descriptions.Item>
            <Descriptions.Item label="BIOS 日期">{hw.bios_date || '-'}</Descriptions.Item>
            <Descriptions.Item label="TPM">
              <BoolTag value={hw.tpm_present} yes={`有${hw.tpm_version ? `（${hw.tpm_version}）` : ''}`} no="無" />
            </Descriptions.Item>
            <Descriptions.Item label="安全開機">
              <BoolTag value={hw.secure_boot} yes="已啟用" no="未啟用" />
            </Descriptions.Item>
            <Descriptions.Item label="BitLocker">
              {hw.bitlocker?.length ? (
                <Flex gap={4} wrap>
                  {hw.bitlocker.map((b) => (
                    <Tag key={b.mount} color={b.protection_status === 'On' ? 'success' : b.protection_status === 'Off' ? 'error' : 'default'}>
                      {b.mount} {b.protection_status === 'On' ? '已加密' : b.protection_status === 'Off' ? '未加密' : '未知'}
                    </Tag>
                  ))}
                </Flex>
              ) : (
                '-'
              )}
            </Descriptions.Item>
          </Descriptions>
        </Card>
      </Col>
      <Col xs={24} xl={12}>
        <Card title="處理器" size="small">
          <Table
            size="small"
            pagination={false}
            rowKey="_k"
            dataSource={withKeys(cpus)}
            columns={[
              { title: '名稱', dataIndex: 'name' },
              { title: '核心', dataIndex: 'cores', width: 70 },
              { title: '執行緒', dataIndex: 'logical_processors', width: 80 },
              { title: '最高時脈', dataIndex: 'max_clock_mhz', width: 100, render: (v: number) => fmtMhz(v) },
            ]}
          />
        </Card>
      </Col>
      <Col xs={24} xl={12}>
        <Card title={`記憶體（共 ${fmtBytes(hw.memory_total, 0)}）`} size="small">
          <Table<MemModule>
            size="small"
            pagination={false}
            rowKey="_k"
            dataSource={withKeys(hw.memory_modules)}
            columns={[
              { title: '插槽', dataIndex: 'slot' },
              { title: '容量', dataIndex: 'capacity', render: (v: number) => fmtBytes(v, 0) },
              { title: '速度', dataIndex: 'speed_mhz', render: (v: number) => (v ? `${v} MHz` : '-') },
              { title: '廠商', dataIndex: 'manufacturer' },
              { title: '料號', dataIndex: 'part_number', ellipsis: true },
            ]}
          />
        </Card>
      </Col>
      <Col span={24}>
        <Card title="磁碟" size="small">
          <Table<Disk>
            size="small"
            pagination={false}
            rowKey="_k"
            dataSource={withKeys(hw.disks)}
            scroll={{ x: 640 }}
            columns={[
              { title: '型號', dataIndex: 'model' },
              { title: '容量', dataIndex: 'size', render: (v: number) => fmtBytes(v, 0) },
              { title: '媒體', dataIndex: 'media_type' },
              { title: '介面', dataIndex: 'bus_type' },
              { title: '序號', dataIndex: 'serial_number', ellipsis: true },
              { title: '健康', dataIndex: 'health', render: (h: string) => <DiskHealthTag health={h} /> },
            ]}
          />
        </Card>
      </Col>
      <Col span={24}>
        <Card title="磁碟區" size="small">
          <VolumeBars volumes={hw.volumes ?? []} />
        </Card>
      </Col>
      <Col span={24}>
        <Card title="網路介面卡" size="small">
          <Table<NIC>
            size="small"
            pagination={false}
            rowKey="_k"
            dataSource={withKeys(hw.nics)}
            scroll={{ x: 900 }}
            columns={[
              { title: '名稱', dataIndex: 'name', ellipsis: true },
              { title: '類型', dataIndex: 'type', width: 90, render: (t: string) => (t === 'wifi' ? 'Wi-Fi' : t === 'ethernet' ? '乙太網路' : t || '-') },
              { title: 'MAC', dataIndex: 'mac', width: 160, render: (v: string) => <span className="mono">{v}</span> },
              { title: 'IP', dataIndex: 'ips', render: (v: string[] | null) => <span className="mono">{joinList(v)}</span> },
              { title: '閘道', dataIndex: 'gateway', render: (v: string[] | null) => joinList(v) },
              { title: 'DNS', dataIndex: 'dns', render: (v: string[] | null) => joinList(v) },
              { title: 'DHCP', dataIndex: 'dhcp', width: 70, render: (v: boolean) => (v ? '是' : '否') },
              { title: '速度', dataIndex: 'speed_mbps', width: 100, render: (v: number) => fmtSpeed(v) },
            ]}
          />
        </Card>
      </Col>
      <Col xs={24} xl={12}>
        <Card title="顯示卡" size="small">
          <Table<GPU>
            size="small"
            pagination={false}
            rowKey="_k"
            dataSource={withKeys(hw.gpus)}
            columns={[
              { title: '名稱', dataIndex: 'name' },
              { title: '驅動版本', dataIndex: 'driver_version' },
              { title: 'VRAM', dataIndex: 'vram', render: (v: number) => (v ? fmtBytes(v, 0) : '-') },
            ]}
          />
        </Card>
      </Col>
      <Col xs={24} xl={12}>
        <Card title="螢幕" size="small">
          <Table<Monitor>
            size="small"
            pagination={false}
            rowKey="_k"
            dataSource={withKeys(hw.monitors)}
            columns={[
              { title: '廠商', dataIndex: 'manufacturer' },
              { title: '型號', dataIndex: 'model' },
              { title: '序號', dataIndex: 'serial_number' },
            ]}
          />
        </Card>
      </Col>
    </Row>
  )
}
