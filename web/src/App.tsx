import { lazy } from 'react'
import { QueryClientProvider } from '@tanstack/react-query'
import { createBrowserRouter, RouterProvider } from 'react-router'
import { Suspense } from 'react'
import { queryClient } from './queryClient'
import { ThemeProvider } from './components/ThemeProvider'
import { AppLayout } from './components/AppLayout'
import { RequireAuth } from './components/RequireAuth'
import { RequireRole } from './components/RoleGate'
import { FullPageSpin } from './components/FullPageSpin'

const Login = lazy(() => import('./pages/Login'))
const Dashboard = lazy(() => import('./pages/Dashboard'))
const DeviceList = lazy(() => import('./pages/devices/DeviceList'))
const DeviceDetail = lazy(() => import('./pages/devices/DeviceDetail'))
const RemoteViewer = lazy(() => import('./pages/remote/RemoteViewer'))
const RemoteSessions = lazy(() => import('./pages/remote/RemoteSessions'))
const Discovery = lazy(() => import('./pages/discovery/Discovery'))
const ScanDetail = lazy(() => import('./pages/discovery/ScanDetail'))
const Packages = lazy(() => import('./pages/deploy/Packages'))
const Deployments = lazy(() => import('./pages/deploy/Deployments'))
const DeploymentDetail = lazy(() => import('./pages/deploy/DeploymentDetail'))
const Scripts = lazy(() => import('./pages/automation/Scripts'))
const Schedules = lazy(() => import('./pages/automation/Schedules'))
const Commands = lazy(() => import('./pages/automation/Commands'))
const Audit = lazy(() => import('./pages/Audit'))
const Settings = lazy(() => import('./pages/settings/Settings'))
const NotFound = lazy(() => import('./pages/NotFound'))

const router = createBrowserRouter([
  {
    path: '/login',
    element: (
      <Suspense fallback={<FullPageSpin />}>
        <Login />
      </Suspense>
    ),
  },
  {
    path: '/remote/:sessionId',
    element: (
      <RequireAuth>
        <Suspense fallback={<FullPageSpin />}>
          <RequireRole min="helpdesk">
            <RemoteViewer />
          </RequireRole>
        </Suspense>
      </RequireAuth>
    ),
  },
  {
    path: '/',
    element: (
      <RequireAuth>
        <AppLayout />
      </RequireAuth>
    ),
    children: [
      { index: true, element: <Dashboard /> },
      { path: 'devices', element: <DeviceList /> },
      { path: 'devices/:id', element: <DeviceDetail /> },
      { path: 'discovery', element: <Discovery /> },
      { path: 'discovery/:id', element: <ScanDetail /> },
      { path: 'packages', element: <Packages /> },
      { path: 'deployments', element: <Deployments /> },
      { path: 'deployments/:id', element: <DeploymentDetail /> },
      { path: 'scripts', element: <Scripts /> },
      { path: 'schedules', element: <Schedules /> },
      { path: 'commands', element: <Commands /> },
      {
        path: 'remote-sessions',
        element: (
          <RequireRole min="helpdesk">
            <RemoteSessions />
          </RequireRole>
        ),
      },
      {
        path: 'audit',
        element: (
          <RequireRole min="admin">
            <Audit />
          </RequireRole>
        ),
      },
      {
        path: 'settings',
        element: (
          <RequireRole min="admin">
            <Settings />
          </RequireRole>
        ),
      },
      { path: '*', element: <NotFound /> },
    ],
  },
])

export default function App() {
  return (
    <ThemeProvider>
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </ThemeProvider>
  )
}
