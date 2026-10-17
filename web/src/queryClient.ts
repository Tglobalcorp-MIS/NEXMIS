import { MutationCache, QueryCache, QueryClient } from '@tanstack/react-query'
import { ApiError } from './api/client'
import { showError } from './utils/feedback'

declare module '@tanstack/react-query' {
  interface Register {
    queryMeta: { silent?: boolean }
    mutationMeta: { silent?: boolean }
  }
}

function report(err: unknown) {
  if (err instanceof ApiError) {
    if (err.status === 401) return
    showError(err.message)
  } else if (err instanceof Error && err.name !== 'AbortError') {
    showError(err.message)
  }
}

export const queryClient = new QueryClient({
  queryCache: new QueryCache({
    onError: (err, query) => {
      if (query.meta?.silent) return
      // 背景重新整理失敗只提示一次（已有資料時避免反覆跳出）
      if (query.state.data !== undefined && query.state.fetchFailureCount > 1) return
      report(err)
    },
  }),
  mutationCache: new MutationCache({
    onError: (err, _v, _c, mutation) => {
      if (mutation.meta?.silent) return
      report(err)
    },
  }),
  defaultOptions: {
    queries: {
      staleTime: 15_000,
      refetchOnWindowFocus: false,
      retry: (count, err) => {
        if (err instanceof ApiError && err.status >= 400 && err.status < 500) return false
        return count < 2
      },
    },
  },
})
