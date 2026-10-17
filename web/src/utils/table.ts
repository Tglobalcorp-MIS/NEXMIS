/** 為沒有唯一鍵的資料列加上索引鍵（antd Table rowKey 不再建議使用 index 參數）。 */
export function withKeys<T extends object>(rows: T[] | null | undefined): (T & { _k: number })[] {
  return (rows ?? []).map((r, i) => ({ ...r, _k: i }))
}
