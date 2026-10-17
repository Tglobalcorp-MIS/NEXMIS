import { Suspense, useMemo, useState } from 'react'
import { Link, Outlet, useLocation, useNavigate } from 'react-router'
import { Avatar, Badge, Button, Dropdown, Flex, Layout, Menu, Space, Tooltip, Typography, theme } from 'antd'
import type { MenuProps } from 'antd'
import {
  ApiOutlined,
  AuditOutlined,
  BellOutlined,
  CalendarOutlined,
  CloudUploadOutlined,
  CodeOutlined,
  DashboardOutlined,
  DesktopOutlined,
  HistoryOutlined,
  KeyOutlined,
  LaptopOutlined,
  LogoutOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
  MoonOutlined,
  RadarChartOutlined,
  SettingOutlined,
  SunOutlined,
  ThunderboltOutlined,
  AppstoreOutlined,
  UserOutlined,
} from '@ant-design/icons'
import type { Role } from '../api/types'
import { useAuth } from '../hooks/useAuth'
import { useEvents } from '../hooks/useEvents'
import { useThemeMode } from '../hooks/useThemeMode'
import { EventsProvider } from './EventsProvider'
import { FullPageSpin } from './FullPageSpin'
import { RoleTag } from './StatusTags'
import { ChangePasswordModal } from './ChangePasswordModal'

const { Header, Sider, Content } = Layout

interface NavItem {
  key: string
  label: string
  icon?: React.ReactNode
  min?: Role
  children?: NavItem[]
}

const nav: NavItem[] = [
  { key: '/', label: '儀表板', icon: <DashboardOutlined /> },
  {
    key: 'g-devices',
    label: '裝置管理',
    icon: <LaptopOutlined />,
    children: [
      { key: '/devices', label: '裝置清單', icon: <DesktopOutlined /> },
      { key: '/discovery', label: '設備探索', icon: <RadarChartOutlined /> },
    ],
  },
  {
    key: 'g-deploy',
    label: '軟體部署',
    icon: <CloudUploadOutlined />,
    children: [
      { key: '/packages', label: '套件庫', icon: <AppstoreOutlined /> },
      { key: '/deployments', label: '部署工作', icon: <ThunderboltOutlined /> },
    ],
  },
  {
    key: 'g-auto',
    label: '自動化',
    icon: <CodeOutlined />,
    children: [
      { key: '/scripts', label: '指令庫', icon: <CodeOutlined /> },
      { key: '/schedules', label: '排程', icon: <CalendarOutlined /> },
      { key: '/commands', label: '指令紀錄', icon: <HistoryOutlined /> },
    ],
  },
  { key: '/remote-sessions', label: '遠端連線紀錄', icon: <ApiOutlined />, min: 'helpdesk' },
  { key: '/audit', label: '稽核紀錄', icon: <AuditOutlined />, min: 'admin' },
  { key: '/settings', label: '系統設定', icon: <SettingOutlined />, min: 'admin' },
]

function toMenu(items: NavItem[], can: (r: Role) => boolean): MenuProps['items'] {
  return items
    .filter((i) => !i.min || can(i.min))
    .map((i) =>
      i.children
        ? { key: i.key, label: i.label, icon: i.icon, children: toMenu(i.children, can) }
        : { key: i.key, label: <Link to={i.key}>{i.label}</Link>, icon: i.icon },
    )
}

const flatKeys = nav.flatMap((n) => (n.children ? n.children.map((c) => ({ key: c.key, parent: n.key })) : [{ key: n.key, parent: '' }]))

function selectedOf(pathname: string) {
  const match = flatKeys
    .filter((k) => (k.key === '/' ? pathname === '/' : pathname === k.key || pathname.startsWith(k.key + '/')))
    .sort((a, b) => b.key.length - a.key.length)[0]
  return match ?? { key: '', parent: '' }
}

function HeaderBar({ collapsed, onToggle }: { collapsed: boolean; onToggle: () => void }) {
  const { user, logout } = useAuth()
  const { connected, unread, totalUnread, clearUnread } = useEvents()
  const { mode, toggle } = useThemeMode()
  const navigate = useNavigate()
  const { token } = theme.useToken()
  const [pwdOpen, setPwdOpen] = useState(false)

  const unreadItems: MenuProps['items'] = Object.entries(unread).length
    ? Object.entries(unread).map(([id, u]) => ({
        key: id,
        label: (
          <Flex justify="space-between" gap={16} style={{ minWidth: 200 }}>
            <span>{u.name}</span>
            <Badge count={u.count} />
          </Flex>
        ),
        onClick: () => {
          clearUnread(id)
          void navigate(`/devices/${id}?tab=chat`)
        },
      }))
    : [{ key: 'none', label: '沒有未讀訊息', disabled: true }]

  const userItems: MenuProps['items'] = [
    {
      key: 'info',
      disabled: true,
      label: (
        <div style={{ color: token.colorText }}>
          <div style={{ fontWeight: 600 }}>{user?.display_name || user?.username}</div>
          <div style={{ fontSize: 12, color: token.colorTextSecondary }}>
            {user?.username}（{user?.source === 'ad' ? 'AD 帳號' : '本機帳號'}）
          </div>
        </div>
      ),
    },
    { type: 'divider' },
    ...(user?.source === 'local'
      ? [{ key: 'pwd', icon: <KeyOutlined />, label: '變更密碼', onClick: () => setPwdOpen(true) }]
      : []),
    { key: 'logout', icon: <LogoutOutlined />, label: '登出', danger: true, onClick: () => void logout() },
  ]

  return (
    <Header
      style={{
        padding: '0 16px',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        borderBottom: `1px solid ${token.colorBorderSecondary}`,
        position: 'sticky',
        top: 0,
        zIndex: 10,
        background: token.colorBgContainer,
      }}
    >
      <Button type="text" icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />} onClick={onToggle} aria-label="切換選單" />
      <Space size={4}>
        <Tooltip title={connected ? '即時更新已連線' : '即時更新中斷，正在重新連線…'}>
          <Badge status={connected ? 'success' : 'warning'} text={<Typography.Text type="secondary" className="nx-hide-sm" style={{ fontSize: 12 }}>{connected ? '即時' : '重新連線中'}</Typography.Text>} style={{ marginRight: 12 }} />
        </Tooltip>
        <Dropdown menu={{ items: unreadItems }} trigger={['click']} placement="bottomRight">
          <Button type="text" aria-label="未讀訊息">
            <Badge count={totalUnread} size="small">
              <BellOutlined style={{ fontSize: 16 }} />
            </Badge>
          </Button>
        </Dropdown>
        <Tooltip title={mode === 'dark' ? '切換為淺色主題' : '切換為深色主題'}>
          <Button type="text" icon={mode === 'dark' ? <SunOutlined /> : <MoonOutlined />} onClick={toggle} aria-label="切換主題" />
        </Tooltip>
        <Dropdown menu={{ items: userItems }} trigger={['click']} placement="bottomRight">
          <Button type="text" style={{ height: 48 }}>
            <Space>
              <Avatar size="small" icon={<UserOutlined />} style={{ background: token.colorPrimary }} />
              <span className="nx-hide-sm">{user?.display_name || user?.username}</span>
              {user && <span className="nx-hide-sm"><RoleTag role={user.role} /></span>}
            </Space>
          </Button>
        </Dropdown>
      </Space>
      <ChangePasswordModal open={pwdOpen} onClose={() => setPwdOpen(false)} />
    </Header>
  )
}

export function AppLayout() {
  const { can } = useAuth()
  const loc = useLocation()
  const [collapsed, setCollapsed] = useState(false)
  const sel = selectedOf(loc.pathname)
  const [openKeys, setOpenKeys] = useState<string[]>(() => (sel.parent ? [sel.parent] : []))
  const items = useMemo(() => toMenu(nav, can), [can])

  return (
    <EventsProvider>
      <Layout style={{ minHeight: '100%' }}>
        <Sider
          collapsible
          collapsed={collapsed}
          onCollapse={setCollapsed}
          breakpoint="lg"
          trigger={null}
          width={220}
          style={{ position: 'sticky', top: 0, height: '100vh', overflow: 'auto' }}
        >
          <Link to="/">
            <Flex align="center" gap={10} style={{ height: 64, padding: collapsed ? '0 24px' : '0 20px', color: '#fff' }}>
              <img src="/favicon.svg" width={28} height={28} alt="" />
              {!collapsed && (
                <div style={{ lineHeight: 1.2 }}>
                  <div style={{ fontWeight: 700, fontSize: 18, letterSpacing: 1 }}>NEXMIS</div>
                  <div style={{ fontSize: 11, opacity: 0.65 }}>MIS 整合管理平台</div>
                </div>
              )}
            </Flex>
          </Link>
          <Menu
            theme="dark"
            mode="inline"
            items={items}
            selectedKeys={[sel.key]}
            openKeys={collapsed ? undefined : openKeys}
            onOpenChange={setOpenKeys}
          />
        </Sider>
        <Layout>
          <HeaderBar collapsed={collapsed} onToggle={() => setCollapsed((c) => !c)} />
          <Content style={{ padding: 20, minWidth: 0 }}>
            <Suspense fallback={<FullPageSpin />}>
              <Outlet />
            </Suspense>
          </Content>
        </Layout>
      </Layout>
    </EventsProvider>
  )
}
