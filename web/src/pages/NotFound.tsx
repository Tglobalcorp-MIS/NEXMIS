import { Button, Result } from 'antd'
import { Link } from 'react-router'

export default function NotFound() {
  return (
    <Result
      status="404"
      title="找不到頁面"
      subTitle="您要找的頁面不存在或已被移除。"
      extra={
        <Link to="/">
          <Button type="primary">回到儀表板</Button>
        </Link>
      }
    />
  )
}
