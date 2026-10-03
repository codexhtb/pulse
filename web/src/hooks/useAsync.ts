import { useCallback, useEffect, useState } from 'react'

interface AsyncState<T> {
  data: T | null
  loading: boolean
  error: Error | null
}

export function useAsync<T>(
  loader: (signal: AbortSignal) => Promise<T>,
  dependencies: readonly unknown[],
  refreshMs?: number,
): AsyncState<T> & { refresh: () => void } {
  const [state, setState] = useState<AsyncState<T>>({
    data: null,
    loading: true,
    error: null,
  })
  const [revision, setRevision] = useState(0)

  const refresh = useCallback(() => setRevision((value) => value + 1), [])

  useEffect(() => {
    const controller = new AbortController()
    setState((current) => ({ ...current, loading: current.data === null, error: null }))

    loader(controller.signal)
      .then((data) => setState({ data, loading: false, error: null }))
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === 'AbortError') return
        setState((current) => ({
          data: current.data,
          loading: false,
          error: error instanceof Error ? error : new Error(String(error)),
        }))
      })

    return () => controller.abort()
    // Dependencies are intentionally supplied by the caller.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [...dependencies, revision])

  useEffect(() => {
    if (!refreshMs) return
    const timer = window.setInterval(refresh, refreshMs)
    return () => window.clearInterval(timer)
  }, [refresh, refreshMs])

  return { ...state, refresh }
}
