import { useCallback, useEffect, useRef, useState } from 'react'

/** 游标分页接口的返回形状（与 lib/api/client 的分页接口一致）。 */
export interface PageOf<T, C> {
  items: T[]
  nextCursor: C | null
}

export interface PagedList<T> {
  items: T[]
  loading: boolean
  loadingMore: boolean
  hasMore: boolean
  loadMore: () => void
  reload: () => void
  setItems: (next: T[] | ((prev: T[]) => T[])) => void
}

/**
 * 游标分页列表的通用加载逻辑：
 * resetKey 变化（筛选条件变化）时从头加载，loadMore 追加下一页。
 * 用 cancelled 标志避免组件卸载/切换筛选后的 setState。
 */
export function usePagedList<T, C extends string | number>(
  resetKey: string,
  fetchPage: (cursor?: C) => Promise<PageOf<T, C>>,
  onError: (e: unknown) => void,
): PagedList<T> {
  const [items, setItems] = useState<T[]>([])
  const [cursor, setCursor] = useState<C | null>(null)
  const [loading, setLoading] = useState(true)
  const [loadingMore, setLoadingMore] = useState(false)
  const [nonce, setNonce] = useState(0)
  /** 每次从头加载都会递增；进行中的 loadMore 若发现代际变化则丢弃结果。 */
  const generation = useRef(0)

  const fetchRef = useRef(fetchPage)
  const errorRef = useRef(onError)
  useEffect(() => {
    fetchRef.current = fetchPage
    errorRef.current = onError
  })

  useEffect(() => {
    let cancelled = false
    generation.current += 1
    void (async () => {
      setLoading(true)
      setLoadingMore(false)
      try {
        const page = await fetchRef.current(undefined)
        if (cancelled) return
        setItems(page.items)
        setCursor(page.nextCursor)
      } catch (e) {
        if (cancelled) return
        errorRef.current(e)
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [resetKey, nonce])

  const loadMore = useCallback(() => {
    if (cursor === null || loadingMore) return
    const gen = generation.current
    void (async () => {
      setLoadingMore(true)
      try {
        const page = await fetchRef.current(cursor)
        if (gen !== generation.current) return
        setItems((prev) => [...prev, ...page.items])
        setCursor(page.nextCursor)
      } catch (e) {
        if (gen === generation.current) errorRef.current(e)
      } finally {
        if (gen === generation.current) setLoadingMore(false)
      }
    })()
  }, [cursor, loadingMore])

  const reload = useCallback(() => {
    setNonce((n) => n + 1)
  }, [])

  return { items, loading, loadingMore, hasMore: cursor !== null, loadMore, reload, setItems }
}

/** 单资源加载：key 变化时重新加载，同样用 cancelled 标志避免卸载后 setState。 */
export function useAsyncData<T>(
  key: string,
  load: () => Promise<T>,
  onError: (e: unknown) => void,
): { data: T | null; loading: boolean; reload: () => void } {
  const [data, setData] = useState<T | null>(null)
  const [loading, setLoading] = useState(true)
  const [nonce, setNonce] = useState(0)

  const loadRef = useRef(load)
  const errorRef = useRef(onError)
  useEffect(() => {
    loadRef.current = load
    errorRef.current = onError
  })

  useEffect(() => {
    let cancelled = false
    void (async () => {
      setLoading(true)
      try {
        const value = await loadRef.current()
        if (cancelled) return
        setData(value)
      } catch (e) {
        if (cancelled) return
        errorRef.current(e)
      } finally {
        if (!cancelled) setLoading(false)
      }
    })()
    return () => {
      cancelled = true
    }
  }, [key, nonce])

  const reload = useCallback(() => {
    setNonce((n) => n + 1)
  }, [])

  return { data, loading, reload }
}
