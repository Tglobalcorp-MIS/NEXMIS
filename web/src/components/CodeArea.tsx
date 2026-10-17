import type { KeyboardEvent } from 'react'
import { Input } from 'antd'

export const monoFont = 'ui-monospace, SFMono-Regular, Consolas, "Cascadia Code", "Liberation Mono", monospace'

/** 等寬字型的指令碼編輯區（Tab 鍵插入空白）。可作為 Form.Item 的受控元件。 */
export function CodeArea({
  value,
  onChange,
  rows = 12,
  placeholder,
}: {
  value?: string
  onChange?: (v: string) => void
  rows?: number
  placeholder?: string
}) {
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key !== 'Tab' || e.shiftKey || e.ctrlKey || e.altKey || e.metaKey) return
    e.preventDefault()
    const el = e.currentTarget
    const start = el.selectionStart
    const end = el.selectionEnd
    const v = value ?? ''
    const next = v.slice(0, start) + '    ' + v.slice(end)
    onChange?.(next)
    requestAnimationFrame(() => {
      el.selectionStart = el.selectionEnd = start + 4
    })
  }
  return (
    <Input.TextArea
      value={value}
      onChange={(e) => onChange?.(e.target.value)}
      onKeyDown={onKeyDown}
      rows={rows}
      placeholder={placeholder}
      spellCheck={false}
      autoComplete="off"
      style={{ fontFamily: monoFont, fontSize: 13, lineHeight: 1.5 }}
    />
  )
}
