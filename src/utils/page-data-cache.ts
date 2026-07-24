type PageDataCacheEntry = {
  value: unknown
  updatedAt: number
}

const pageDataCache = new Map<string, PageDataCacheEntry>()

export function getPageDataCache<T>(key: string): T | undefined {
  return pageDataCache.get(key)?.value as T | undefined
}

export function setPageDataCache<T>(key: string, value: T): void {
  pageDataCache.set(key, {
    value,
    updatedAt: Date.now()
  })
}

export function clearPageDataCache(): void {
  pageDataCache.clear()
}
