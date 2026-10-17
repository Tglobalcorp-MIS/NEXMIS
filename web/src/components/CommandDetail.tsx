import { Descriptions, Typography } from 'antd'
import type { Command } from '../api/types'
import { fmtTime } from '../utils/format'
import { commandKindLabels, labelOf } from '../utils/labels'
import { JsonView, OutputBlock } from './JsonView'
import { CommandStatusTag } from './StatusTags'

/** 指令結果展開檢視：參數、stdout、stderr。 */
export function CommandDetail({ command: c }: { command: Command }) {
  const args = c.args ?? {}
  const script = typeof args.script === 'string' ? args.script : undefined
  const rest = Object.fromEntries(Object.entries(args).filter(([k]) => k !== 'script'))
  return (
    <div style={{ padding: '4px 8px' }}>
      <Descriptions size="small" column={{ xs: 1, sm: 2, md: 2, lg: 4, xl: 4, xxl: 4 }} style={{ marginBottom: 12 }}>
        <Descriptions.Item label="類型">{labelOf(commandKindLabels, c.kind)}</Descriptions.Item>
        <Descriptions.Item label="狀態">
          <CommandStatusTag status={c.status} />
        </Descriptions.Item>
        <Descriptions.Item label="結束碼">{c.exit_code ?? '-'}</Descriptions.Item>
        <Descriptions.Item label="執行者">{c.issued_by_name || '-'}</Descriptions.Item>
        <Descriptions.Item label="建立">{fmtTime(c.created_at, true)}</Descriptions.Item>
        <Descriptions.Item label="開始">{fmtTime(c.started_at, true)}</Descriptions.Item>
        <Descriptions.Item label="完成">{fmtTime(c.finished_at, true)}</Descriptions.Item>
        <Descriptions.Item label="指令 ID">
          <Typography.Text copyable style={{ fontSize: 12 }} className="mono">
            {c.id}
          </Typography.Text>
        </Descriptions.Item>
      </Descriptions>
      {script !== undefined && <OutputBlock label="指令碼" text={script} />}
      {Object.keys(rest).length > 0 && (
        <div style={{ marginBottom: 8 }}>
          <Typography.Text type="secondary" style={{ fontSize: 12 }}>
            參數
          </Typography.Text>
          <JsonView value={rest} maxHeight={200} />
        </div>
      )}
      <OutputBlock label="標準輸出（stdout）" text={c.stdout} />
      {c.stderr && <OutputBlock label="標準錯誤（stderr）" text={c.stderr} error />}
      {c.truncated && <Typography.Text type="warning">輸出超過 1 MB，已截斷。</Typography.Text>}
    </div>
  )
}
