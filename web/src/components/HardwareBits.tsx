import { Empty, Flex, Progress, Tag, Typography } from 'antd'
import type { Volume } from '../api/types'
import { fmtBytes } from '../utils/format'

export function VolumeBars({ volumes }: { volumes: Volume[] }) {
  if (!volumes.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} />
  return (
    <Flex vertical gap={12}>
      {volumes.map((v) => {
        const used = v.size - v.free
        const pct = v.size ? Math.round((used / v.size) * 100) : 0
        return (
          <div key={v.mount}>
            <Flex justify="space-between" wrap>
              <Typography.Text strong>
                {v.mount} {v.label && <Typography.Text type="secondary">（{v.label}）</Typography.Text>}
                <Tag style={{ marginLeft: 8 }}>{v.file_system || '-'}</Tag>
              </Typography.Text>
              <Typography.Text type="secondary">
                已用 {fmtBytes(used)} / {fmtBytes(v.size)}，剩餘 {fmtBytes(v.free)}
              </Typography.Text>
            </Flex>
            <Progress percent={pct} status={pct >= 90 ? 'exception' : 'normal'} strokeColor={pct >= 90 ? undefined : pct >= 80 ? '#faad14' : undefined} />
          </div>
        )
      })}
    </Flex>
  )
}

export function DiskHealthTag({ health: h }: { health: string }) {
  const color = h === 'Healthy' ? 'success' : h === 'Warning' ? 'warning' : h === 'Unhealthy' ? 'error' : 'default'
  const text = h === 'Healthy' ? '良好' : h === 'Warning' ? '警告' : h === 'Unhealthy' ? '異常' : '未知'
  return <Tag color={color}>{text}</Tag>
}

