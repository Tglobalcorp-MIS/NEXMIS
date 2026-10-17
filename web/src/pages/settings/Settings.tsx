import { lazy, Suspense } from 'react'
import { useSearchParams } from 'react-router'
import { Tabs } from 'antd'
import { PageHeader } from '../../components/PageHeader'
import { FullPageSpin } from '../../components/FullPageSpin'

const GeneralSettings = lazy(() => import('./GeneralSettings'))
const AgentSettings = lazy(() => import('./AgentSettings'))
const ADSettings = lazy(() => import('./ADSettings'))
const EnrollmentTokens = lazy(() => import('./EnrollmentTokens'))
const Users = lazy(() => import('./Users'))
const Groups = lazy(() => import('./Groups'))
const SystemInfo = lazy(() => import('./SystemInfo'))

const tabs = [
  { key: 'general', label: '一般設定', el: <GeneralSettings /> },
  { key: 'agent', label: 'Agent 設定', el: <AgentSettings /> },
  { key: 'ad', label: 'AD 整合', el: <ADSettings /> },
  { key: 'tokens', label: '註冊權杖', el: <EnrollmentTokens /> },
  { key: 'users', label: '使用者管理', el: <Users /> },
  { key: 'groups', label: '裝置群組', el: <Groups /> },
  { key: 'system', label: '系統資訊', el: <SystemInfo /> },
]

export default function Settings() {
  const [sp, setSp] = useSearchParams()
  const tab = sp.get('tab') ?? 'general'
  return (
    <>
      <PageHeader title="系統設定" />
      <Tabs
        activeKey={tabs.some((t) => t.key === tab) ? tab : 'general'}
        onChange={(k) => setSp({ tab: k }, { replace: true })}
        destroyOnHidden
        items={tabs.map((t) => ({ key: t.key, label: t.label, children: <Suspense fallback={<FullPageSpin />}>{t.el}</Suspense> }))}
      />
    </>
  )
}
