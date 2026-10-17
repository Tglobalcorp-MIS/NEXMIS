import { useMemo, useState } from 'react'
import { useQueries, useQuery } from '@tanstack/react-query'
import { Select, Space, Typography } from 'antd'
import { deviceApi, packageApi } from '../api/endpoints'
import { useGroups } from '../hooks/useGroups'
import { qk } from '../api/keys'
import type { Device, DeviceListParams } from '../api/types'
import { useDebounced } from '../hooks/useDebounced'
import { OnlineStatus } from './StatusTags'

interface DeviceSelectProps {
  value?: string[]
  onChange?: (v: string[]) => void
  placeholder?: string
  filter?: Partial<DeviceListParams>
  style?: React.CSSProperties
}

/** 多選裝置（伺服器端搜尋）。 */
export function DeviceMultiSelect({ value, onChange, placeholder = '搜尋名稱、IP、序號或使用者', filter, style }: DeviceSelectProps) {
  const [search, setSearch] = useState('')
  const q = useDebounced(search, 300)
  const params = { q: q || undefined, page: 1, page_size: 30, ...filter }
  const { data, isFetching } = useQuery({
    queryKey: qk.deviceList(params),
    queryFn: () => deviceApi.list(params),
    staleTime: 30_000,
  })
  const ids = useMemo(() => value ?? [], [value])
  const found = data?.items
  // 已選取但不在目前搜尋結果中的裝置，個別查名稱（上限 50 台）
  const missing = ids.filter((id) => !found?.some((d) => d.id === id)).slice(0, 50)
  const extra = useQueries({
    queries: missing.map((id) => ({
      queryKey: qk.deviceDetail(id),
      queryFn: () => deviceApi.get(id),
      staleTime: 60_000,
    })),
  })
  const options = useMemo(() => {
    const map = new Map<string, Device>()
    found?.forEach((d) => map.set(d.id, d))
    extra.forEach((r) => {
      if (r.data) map.set(r.data.id, r.data)
    })
    return [...map.values()].map((d) => ({
      value: d.id,
      label: d.name,
      device: d,
    }))
  }, [found, extra])

  return (
    <Select
      mode="multiple"
      value={ids}
      onChange={onChange}
      placeholder={placeholder}
      showSearch={{ filterOption: false, onSearch: setSearch, searchValue: search }}
      loading={isFetching}
      options={options}
      optionRender={(o) => {
        const d = (o.data as { device: Device }).device
        return (
          <Space>
            <OnlineStatus online={d.online} hasAgent={d.has_agent} showText={false} />
            <span>{d.name}</span>
            <Typography.Text type="secondary" style={{ fontSize: 12 }}>
              {d.ip_addresses?.[0] ?? ''} {d.logged_in_user}
            </Typography.Text>
          </Space>
        )
      }}
      style={{ width: '100%', ...style }}
      maxTagCount="responsive"
      notFoundContent={isFetching ? '搜尋中…' : '查無裝置'}
      allowClear
    />
  )
}

/** 單選裝置。 */
export function DeviceSingleSelect({
  value,
  onChange,
  placeholder,
  filter,
}: {
  value?: string
  onChange?: (v: string | undefined) => void
  placeholder?: string
  filter?: Partial<DeviceListParams>
}) {
  return (
    <DeviceMultiSelect
      value={value ? [value] : []}
      onChange={(v) => onChange?.(v.length ? v[v.length - 1] : undefined)}
      placeholder={placeholder}
      filter={filter}
    />
  )
}

export function GroupSelect({
  value,
  onChange,
  multiple,
  placeholder = '選擇群組',
  allowNone,
}: {
  value?: string | string[] | null
  onChange?: (v: string | string[] | null) => void
  multiple?: boolean
  placeholder?: string
  allowNone?: boolean
}) {
  const { data, isLoading } = useGroups()
  const options = [
    ...(allowNone && !multiple ? [{ value: '__none__', label: '（無群組）' }] : []),
    ...(data ?? []).map((g) => ({ value: g.id, label: `${g.name}（${g.device_count}）` })),
  ]
  return (
    <Select
      mode={multiple ? 'multiple' : undefined}
      value={value === null && allowNone ? '__none__' : (value ?? undefined)}
      onChange={(v: string | string[]) => onChange?.(v === '__none__' ? null : (v ?? null))}
      options={options}
      loading={isLoading}
      placeholder={placeholder}
      allowClear
      showSearch={{ optionFilterProp: 'label' }}
      style={{ width: '100%' }}
    />
  )
}

export function TagsInput({
  value,
  onChange,
  placeholder = '輸入標籤後按 Enter',
}: {
  value?: string[]
  onChange?: (v: string[]) => void
  placeholder?: string
}) {
  return (
    <Select
      mode="tags"
      value={value ?? []}
      onChange={onChange}
      tokenSeparators={[',', '，']}
      placeholder={placeholder}
      open={false}
      suffixIcon={null}
      style={{ width: '100%' }}
    />
  )
}

export function PackageSelect({ value, onChange }: { value?: string; onChange?: (v: string) => void }) {
  const { data, isLoading } = useQuery({ queryKey: qk.packages, queryFn: packageApi.list })
  return (
    <Select
      value={value}
      onChange={onChange}
      loading={isLoading}
      placeholder="選擇軟體套件"
      showSearch={{ optionFilterProp: 'label' }}
      options={(data ?? []).map((p) => ({ value: p.id, label: `${p.name} ${p.version}（${p.type.toUpperCase()}）` }))}
      style={{ width: '100%' }}
    />
  )
}
