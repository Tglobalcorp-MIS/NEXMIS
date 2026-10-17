import { theme } from 'antd'

export function JsonView({ value, maxHeight = 400 }: { value: unknown; maxHeight?: number }) {
  const { token } = theme.useToken()
  let text: string
  try {
    text = typeof value === 'string' ? value : JSON.stringify(value, null, 2)
  } catch {
    text = String(value)
  }
  return (
    <pre
      style={{
        margin: 0,
        padding: 12,
        maxHeight,
        overflow: 'auto',
        background: token.colorFillTertiary,
        borderRadius: token.borderRadius,
        fontFamily: 'ui-monospace, SFMono-Regular, Consolas, "Liberation Mono", monospace',
        fontSize: 12,
        whiteSpace: 'pre-wrap',
        wordBreak: 'break-all',
      }}
    >
      {text ?? 'null'}
    </pre>
  )
}

export function OutputBlock({ text, label, error }: { text: string; label: string; error?: boolean }) {
  const { token } = theme.useToken()
  return (
    <div style={{ marginBottom: 8 }}>
      <div style={{ fontSize: 12, color: token.colorTextSecondary, marginBottom: 4 }}>{label}</div>
      <pre
        style={{
          margin: 0,
          padding: 12,
          maxHeight: 360,
          overflow: 'auto',
          background: '#141414',
          color: error ? '#ff7875' : '#d9d9d9',
          borderRadius: token.borderRadius,
          fontFamily: 'ui-monospace, SFMono-Regular, Consolas, "Liberation Mono", monospace',
          fontSize: 12,
          whiteSpace: 'pre-wrap',
          wordBreak: 'break-all',
        }}
      >
        {text || '（無輸出）'}
      </pre>
    </div>
  )
}
