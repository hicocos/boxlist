// Bounded, in-memory ownership of already-loaded images. No fetching, blobs,
// permanent storage or unbounded whole-book preload belongs in this cache.
export interface ReaderCacheLimits { maxBytes: number; maxEntries: number }
const MiB = 1024 * 1024
export function readerCacheLimits(mobile: boolean, deviceMemory?: number): ReaderCacheLimits {
  // deviceMemory is an optional coarse browser hint, not server memory.
  const lowMemory = Number.isFinite(deviceMemory) && deviceMemory! > 0 && deviceMemory! <= 2
  return { maxBytes: (lowMemory ? 128 : mobile ? 256 : 512) * MiB, maxEntries: lowMemory ? 48 : mobile ? 96 : 160 }
}
export function readerImageCost(width: number, height: number, transferBytes: number): number {
  if (!Number.isFinite(width) || !Number.isFinite(height) || width <= 0 || height <= 0) return Number.POSITIVE_INFINITY
  // Conservative RGBA decode estimate plus compressed bytes; browser internals
  // may discard/redecode pixels and have additional GPU/allocator overhead.
  const decoded = Math.ceil(width) * Math.ceil(height) * 4
  return decoded + (Number.isFinite(transferBytes) ? Math.max(0, Math.ceil(transferBytes)) : 0)
}
export function readerCacheKey(identity: string, url: string): string { return JSON.stringify([identity, url]) }

export class ReaderReadyCache<T> {
  private entries = new Map<string, { value: T; bytes: number }>()
  private used = 0
  private limits: ReaderCacheLimits
  private dispose: (value: T) => void
  constructor(limits: ReaderCacheLimits, dispose: (value: T) => void) {
    this.limits = limits
    this.dispose = dispose
  }
  get bytes() { return this.used }
  get size() { return this.entries.size }
  peek(key: string): T | undefined { return this.entries.get(key)?.value }
  get(key: string): T | undefined {
    const entry = this.entries.get(key)
    if (!entry) return
    this.entries.delete(key)
    this.entries.set(key, entry)
    return entry.value
  }
  put(key: string, value: T, bytes: number) {
    const old = this.entries.get(key)
    if (old) {
      this.used -= old.bytes
      this.entries.delete(key)
      if (old.value !== value) this.dispose(old.value)
    }
    // Invalid raster geometry is never retained. Valid but over-budget visible
    // images may temporarily remain protected until the reader scrolls away.
    if (!Number.isFinite(bytes) || bytes < 0) { this.dispose(value); return }
    this.entries.set(key, { value, bytes })
    this.used += bytes
  }
  trim(protectedKeys: ReadonlySet<string> = new Set()) {
    for (const key of this.entries.keys()) {
      if (this.used <= this.limits.maxBytes && this.entries.size <= this.limits.maxEntries) break
      if (!protectedKeys.has(key)) this.delete(key)
    }
  }
  delete(key: string) {
    const entry = this.entries.get(key)
    if (!entry) return
    this.entries.delete(key)
    this.used -= entry.bytes
    this.dispose(entry.value)
  }
  clear() {
    const previous = [...this.entries.values()]
    this.entries.clear()
    this.used = 0
    for (const entry of previous) this.dispose(entry.value)
  }
}
