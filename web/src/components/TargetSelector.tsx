import { useState } from 'react'
import { Alert, Radio, Space } from 'antd'
import type { Target } from '../api/types'
import { emptyTarget } from '../utils/target'
import { DeviceMultiSelect, GroupSelect, TagsInput } from './Selectors'

type Mode = 'all' | 'devices' | 'groups' | 'tags'


function modeOf(t?: Target): Mode {
  if (!t) return 'devices'
  if (t.all) return 'all'
  if (t.group_ids?.length) return 'groups'
  if (t.tags?.length) return 'tags'
  return 'devices'
}

/** 目標選擇器：全部 / 指定裝置 / 群組 / 標籤。可作為 Form.Item 受控元件。 */
export function TargetSelector({ value, onChange }: { value?: Target; onChange?: (t: Target) => void }) {
  const [mode, setMode] = useState<Mode>(() => modeOf(value))
  const t = value ?? emptyTarget()

  const changeMode = (m: Mode) => {
    setMode(m)
    onChange?.({ ...emptyTarget(), all: m === 'all' })
  }

  return (
    <Space orientation="vertical" style={{ width: '100%' }}>
      <Radio.Group value={mode} onChange={(e) => changeMode(e.target.value as Mode)} optionType="button" buttonStyle="solid">
        <Radio.Button value="all">全部裝置</Radio.Button>
        <Radio.Button value="devices">指定裝置</Radio.Button>
        <Radio.Button value="groups">群組</Radio.Button>
        <Radio.Button value="tags">標籤</Radio.Button>
      </Radio.Group>
      {mode === 'all' && <Alert type="warning" showIcon title="將對所有已安裝 Agent 的裝置執行，請謹慎確認。" />}
      {mode === 'devices' && (
        <DeviceMultiSelect value={t.device_ids} onChange={(ids) => onChange?.({ ...emptyTarget(), device_ids: ids })} />
      )}
      {mode === 'groups' && (
        <GroupSelect
          multiple
          value={t.group_ids}
          onChange={(ids) => onChange?.({ ...emptyTarget(), group_ids: (ids as string[] | null) ?? [] })}
        />
      )}
      {mode === 'tags' && (
        <TagsInput value={t.tags} onChange={(tags) => onChange?.({ ...emptyTarget(), tags })} placeholder="輸入標籤（符合任一標籤的裝置）" />
      )}
    </Space>
  )
}
