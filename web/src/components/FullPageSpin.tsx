import { Flex, Spin } from 'antd'

export function FullPageSpin({ tip = '載入中…' }: { tip?: string }) {
  return (
    <Flex align="center" justify="center" style={{ height: '100%', minHeight: 240 }}>
      <Spin size="large" description={tip}>
        <div style={{ width: 120, height: 60 }} />
      </Spin>
    </Flex>
  )
}
