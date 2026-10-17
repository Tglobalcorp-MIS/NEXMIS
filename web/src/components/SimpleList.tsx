import type { ReactNode } from 'react'
import { Empty, Flex, Typography, theme } from 'antd'

/** 輕量清單（取代已棄用的 antd List）。 */
export function SimpleList<T>({
  items,
  render,
  rowKey,
  empty = '沒有資料',
  maxHeight,
}: {
  items: T[]
  render: (item: T, index: number) => ReactNode
  rowKey: (item: T, index: number) => string
  empty?: ReactNode
  maxHeight?: number
}) {
  const { token } = theme.useToken()
  if (!items.length) return <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={empty} />
  return (
    <div className={maxHeight ? 'nx-scroll' : undefined} style={maxHeight ? { maxHeight, overflow: 'auto' } : undefined}>
      {items.map((it, i) => (
        <div key={rowKey(it, i)} style={{ padding: '10px 4px', borderBottom: i < items.length - 1 ? `1px solid ${token.colorSplit}` : undefined }}>
          {render(it, i)}
        </div>
      ))}
    </div>
  )
}

export function ListMeta({ avatar, title, description }: { avatar?: ReactNode; title: ReactNode; description?: ReactNode }) {
  return (
    <Flex gap={12} align="flex-start">
      {avatar}
      <div style={{ minWidth: 0 }}>
        <div style={{ fontWeight: 500 }}>{title}</div>
        {description && <Typography.Text type="secondary">{description}</Typography.Text>}
      </div>
    </Flex>
  )
}
