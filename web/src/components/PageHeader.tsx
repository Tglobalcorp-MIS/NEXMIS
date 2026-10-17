import type { ReactNode } from 'react'
import { Flex, Typography } from 'antd'

export function PageHeader({
  title,
  subtitle,
  extra,
  back,
}: {
  title: ReactNode
  subtitle?: ReactNode
  extra?: ReactNode
  back?: ReactNode
}) {
  return (
    <Flex justify="space-between" align="center" wrap gap={12} style={{ marginBottom: 16 }}>
      <Flex align="center" gap={8} style={{ minWidth: 0 }}>
        {back}
        <div style={{ minWidth: 0 }}>
          <Typography.Title level={4} style={{ margin: 0 }}>
            {title}
          </Typography.Title>
          {subtitle && <Typography.Text type="secondary">{subtitle}</Typography.Text>}
        </div>
      </Flex>
      {extra && (
        <Flex gap={8} wrap>
          {extra}
        </Flex>
      )}
    </Flex>
  )
}
