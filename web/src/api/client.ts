import type { ApiErrorBody, ListResponse } from './types'

export const API_BASE = '/api/v1'

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

type Query = Record<string, string | number | boolean | null | undefined | string[]>

export function buildQuery(params?: Query): string {
  if (!params) return ''
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === '') continue
    if (Array.isArray(v)) v.forEach((x) => sp.append(k, x))
    else sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}

let redirecting = false

/** 401：導向登入頁（登入頁本身與登入 API 除外）。 */
function handleUnauthorized(path: string) {
  if (path.startsWith('/auth/login')) return
  if (redirecting) return
  const loc = window.location
  if (loc.pathname === '/login') return
  redirecting = true
  const next = encodeURIComponent(loc.pathname + loc.search)
  loc.assign(`/login?next=${next}`)
}

async function parseError(res: Response): Promise<ApiError> {
  let code = 'internal'
  let message = `請求失敗（HTTP ${res.status}）`
  try {
    const body = (await res.json()) as Partial<ApiErrorBody>
    if (body.error) {
      code = body.error.code || code
      message = body.error.message || message
    }
  } catch {
    if (res.status === 502 || res.status === 503 || res.status === 504) message = '無法連線至伺服器'
  }
  return new ApiError(res.status, code, message)
}

export interface RequestOptions {
  query?: Query
  body?: unknown
  signal?: AbortSignal
}

export async function request<T>(method: string, path: string, opts: RequestOptions = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  let body: BodyInit | undefined
  if (method !== 'GET' && method !== 'HEAD') headers['X-NEXMIS-CSRF'] = '1'
  if (opts.body instanceof FormData) {
    body = opts.body
  } else if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json'
    body = JSON.stringify(opts.body)
  }

  let res: Response
  try {
    res = await fetch(API_BASE + path + buildQuery(opts.query), {
      method,
      headers,
      body,
      credentials: 'same-origin',
      signal: opts.signal,
    })
  } catch (e) {
    if (e instanceof DOMException && e.name === 'AbortError') throw e
    throw new ApiError(0, 'network', '無法連線至伺服器，請檢查網路')
  }

  if (res.status === 401) {
    handleUnauthorized(path)
    throw await parseError(res)
  }
  if (!res.ok) throw await parseError(res)
  if (res.status === 204) return undefined as T
  const text = await res.text()
  if (!text) return undefined as T
  return JSON.parse(text) as T
}

export const http = {
  get: <T>(path: string, query?: Query, signal?: AbortSignal) => request<T>('GET', path, { query, signal }),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, { body: body ?? {} }),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, { body }),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, { body }),
  del: <T = void>(path: string) => request<T>('DELETE', path),
}

/** 部分列表端點文件寫 `T[]`、部分為 `{items,total}`，一律正規化。 */
export function normalizeList<T>(data: T[] | ListResponse<T> | null | undefined): ListResponse<T> {
  if (!data) return { items: [], total: 0 }
  if (Array.isArray(data)) return { items: data, total: data.length }
  return { items: data.items ?? [], total: data.total ?? data.items?.length ?? 0 }
}

/** multipart 上傳（使用 XHR 以取得進度）。 */
export function upload<T>(path: string, form: FormData, onProgress?: (percent: number) => void): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', API_BASE + path)
    xhr.setRequestHeader('X-NEXMIS-CSRF', '1')
    xhr.setRequestHeader('Accept', 'application/json')
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable && onProgress) onProgress(Math.round((e.loaded / e.total) * 100))
    }
    xhr.onerror = () => reject(new ApiError(0, 'network', '上傳失敗：無法連線至伺服器'))
    xhr.onload = () => {
      let parsed: unknown = undefined
      try {
        parsed = xhr.responseText ? JSON.parse(xhr.responseText) : undefined
      } catch {
        /* ignore */
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(parsed as T)
        return
      }
      if (xhr.status === 401) handleUnauthorized(path)
      const err = (parsed as Partial<ApiErrorBody> | undefined)?.error
      reject(new ApiError(xhr.status, err?.code ?? 'internal', err?.message ?? `上傳失敗（HTTP ${xhr.status}）`))
    }
    xhr.send(form)
  })
}

export function wsUrl(path: string): string {
  const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${window.location.host}${API_BASE}${path}`
}
