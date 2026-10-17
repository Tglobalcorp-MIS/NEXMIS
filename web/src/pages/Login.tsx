import { useState } from 'react'
import { useNavigate, useSearchParams, Navigate } from 'react-router'
import { useQueryClient } from '@tanstack/react-query'
import { Alert, Button, Card, Flex, Form, Input, Typography, theme } from 'antd'
import { LockOutlined, MoonOutlined, SunOutlined, UserOutlined } from '@ant-design/icons'
import { authApi } from '../api/endpoints'
import { ApiError } from '../api/client'
import { qk } from '../api/keys'
import { useMe } from '../hooks/useAuth'
import { useThemeMode } from '../hooks/useThemeMode'

export default function Login() {
  const [sp] = useSearchParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { token } = theme.useToken()
  const { mode, toggle } = useThemeMode()
  const { data: me } = useMe()
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const next = sp.get('next')
  const target = next && next.startsWith('/') && !next.startsWith('//') ? next : '/'

  if (me) return <Navigate to={target} replace />

  const onFinish = async (v: { username: string; password: string }) => {
    setError(null)
    setLoading(true)
    try {
      const user = await authApi.login({ username: v.username.trim(), password: v.password })
      qc.setQueryData(qk.me, user)
      void navigate(target, { replace: true })
    } catch (e) {
      if (e instanceof ApiError) {
        if (e.status === 401) setError(e.message || '帳號或密碼錯誤')
        else if (e.code === 'rate_limited') setError(e.message || '嘗試次數過多，請稍後再試')
        else setError(e.message)
      } else setError('登入失敗')
    } finally {
      setLoading(false)
    }
  }

  return (
    <Flex
      align="center"
      justify="center"
      style={{
        minHeight: '100%',
        padding: 16,
        background:
          mode === 'dark'
            ? 'radial-gradient(circle at 20% 20%, #10213f 0%, #000 70%)'
            : 'radial-gradient(circle at 20% 20%, #e6f4ff 0%, #f5f5f5 70%)',
      }}
    >
      <Button
        type="text"
        icon={mode === 'dark' ? <SunOutlined /> : <MoonOutlined />}
        onClick={toggle}
        style={{ position: 'fixed', top: 16, right: 16 }}
        aria-label="切換主題"
      />
      <Card style={{ width: 400, maxWidth: '100%', boxShadow: token.boxShadowSecondary }}>
        <Flex vertical align="center" gap={4} style={{ marginBottom: 24 }}>
          <img src="/favicon.svg" width={48} height={48} alt="NEXMIS" />
          <Typography.Title level={3} style={{ margin: '8px 0 0' }}>
            NEXMIS
          </Typography.Title>
          <Typography.Text type="secondary">企業級 MIS 整合管理平台</Typography.Text>
        </Flex>
        {error && <Alert type="error" showIcon title={error} style={{ marginBottom: 16 }} />}
        <Form layout="vertical" onFinish={(v) => void onFinish(v)} requiredMark={false} size="large">
          <Form.Item
            name="username"
            label="帳號"
            rules={[{ required: true, message: '請輸入帳號' }]}
            extra="可使用 AD 帳號：user、DOMAIN\user 或 user@domain"
          >
            <Input prefix={<UserOutlined />} autoComplete="username" autoFocus placeholder="帳號" />
          </Form.Item>
          <Form.Item name="password" label="密碼" rules={[{ required: true, message: '請輸入密碼' }]}>
            <Input.Password prefix={<LockOutlined />} autoComplete="current-password" placeholder="密碼" />
          </Form.Item>
          <Button type="primary" htmlType="submit" block loading={loading}>
            登入
          </Button>
        </Form>
      </Card>
    </Flex>
  )
}
