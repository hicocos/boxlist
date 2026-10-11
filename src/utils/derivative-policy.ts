import type { Derivative, Derivatives, DerivativeJob } from "../types/derivatives"

/** Reject obfuscated provider tokens, protocol-relative URLs and executable schemes. */
export function safeThumbnailUrl(value: unknown): string | undefined {
  if (typeof value !== "string" || !value || /[\s\\\u0000-\u001f]/.test(value)) return
  if (value.startsWith("/") && !value.startsWith("//")) return value
  if (!/^https?:\/\//i.test(value)) return
  try {
    const url = new URL(value)
    if (!url.hostname || url.username || url.password) return
    return value
  } catch { return }
}
export function readyDerivative(value?: Derivative): Derivative | undefined {
  return value?.state === "ready" && safeThumbnailUrl(value.url) ? value : undefined
}
export function thumbnailSource(obj: { thumb?: string; derivatives?: Derivatives }) {
  return readyDerivative(obj.derivatives?.thumb)?.url || safeThumbnailUrl(obj.thumb)
}
export type ReaderQuality = "preview" | "original"
export const readerQuality = (value: unknown): ReaderQuality => value === "original" ? "original" : "preview"
export interface MediaSource {
  url: string
  bytes: number
  kind: "preview" | "upstream" | "original"
  width?: number
  height?: number
}
export interface DerivativeStatusResult { code: number; derivatives?: Derivatives }
export interface DerivativeMedia {
  source: (quality?: ReaderQuality) => MediaSource | undefined
  recover: (failedUrl: string, signal?: AbortSignal) => Promise<MediaSource | undefined>
  blocked: () => boolean
}
/** One status refresh per image owner; permission/unknown failures fail closed.
 * No generation, global ticket cache, unbounded onerror chain or original prefetch. */
export function createDerivativeMedia(options: {
  profile: "thumb" | "reader"
  derivatives?: Derivatives
  upstream?: string
  original?: string
  originalBytes?: number
  refresh: (signal?: AbortSignal) => Promise<DerivativeStatusResult>
  probe?: (url: string, signal?: AbortSignal) => Promise<number>
}): DerivativeMedia {
  let derivatives = options.derivatives
  const failed = new Set<string>()
  let refreshed = false
  let blocked = false
  let pending: Promise<MediaSource | undefined> | undefined
  let probed = false
  let unverified = false
  const source = (quality: ReaderQuality = "preview"): MediaSource | undefined => {
    if (blocked || unverified) return
    const preview = quality === "preview" && readyDerivative(derivatives?.[options.profile])
    if (preview && !failed.has(preview.url)) return {
      url: preview.url, bytes: Math.max(0, preview.bytes || 0), kind: "preview",
      width: preview.width, height: preview.height,
    }
    const upstream = quality === "preview" && options.profile === "thumb" && safeThumbnailUrl(options.upstream)
    if (upstream && !failed.has(upstream)) return { url: upstream, bytes: 0, kind: "upstream" }
    if (options.original && !failed.has(options.original)) return {
      url: options.original, bytes: Math.max(0, options.originalBytes || 0), kind: "original",
    }
  }
  const verifyFailedPreview = async (url: string, signal?: AbortSignal) => {
    if (probed) { blocked = true; return }
    probed = true
    unverified = true
    const code = await options.probe?.(url, signal).catch(() => 0)
    if (signal?.aborted) { probed = false; unverified = false; failed.delete(url); return }
    if (![200, 206, 404, 410].includes(code || 0)) { blocked = true; return }
    unverified = false
    return source()
  }
  const recover = async (failedUrl: string, signal?: AbortSignal): Promise<MediaSource | undefined> => {
    if (signal?.aborted || blocked) return
    if (pending) return pending
    // A failed original never loops back to a smaller preview; explicit retries remain possible.
    if (failedUrl === options.original) return
    failed.add(failedUrl)
    if (refreshed) return verifyFailedPreview(failedUrl, signal)
    refreshed = true
    unverified = true
    pending = (async () => {
      try {
        const result = await options.refresh(signal)
        if (signal?.aborted) { refreshed = false; unverified = false; failed.delete(failedUrl); return }
        if (result.code !== 200) { blocked = true; return }
        unverified = false
        derivatives = result.derivatives
        const renewed = readyDerivative(derivatives?.[options.profile])
        // A ready-but-failing identical ticket must not cause a loop or bypass.
        if (renewed && failed.has(renewed.url)) return verifyFailedPreview(renewed.url, signal)
        return source()
      } catch {
        if (signal?.aborted) { refreshed = false; unverified = false; failed.delete(failedUrl) }
        else blocked = true
        return
      }
      finally { pending = undefined }
    })()
    return pending
  }
  return { source, recover, blocked: () => blocked }
}

export const derivativeJobActive = (state: string) =>
  ["queued", "scanning", "processing", "running", "pending", "retry_wait", "pausing", "cancelling"].includes(state)
export const terminalDerivativeJob = (state: string) => ["completed", "completed_with_errors", "partial_failed", "failed", "cancelled", "canceled"].includes(state)
export function derivativeJobActions(state: string): string[] {
  if (state === "paused") return ["resume", "cancel", "rescan"]
  if (derivativeJobActive(state)) return ["pause", "cancel"]
  return ["retry", "rescan"]
}
/** Scanning/partial coverage has no honest percentage denominator. */
export function derivativeProgress(job: DerivativeJob): number | undefined {
  const stats = job.stats || {}
  if (!job.scan_complete || (stats.failed_dirs || 0) > 0 || job.state === "cancelled") return
  const total = stats.discovered || 0
  if (!total) return job.state === "completed" ? 100 : 0
  const settled = (stats.completed_files || 0) + (stats.partial_files || 0) +
    (stats.skipped_files || 0) + (stats.failed_files || 0)
  return Math.min(100, Math.floor(100 * settled / total))
}
export function validDerivativePath(path: string): boolean {
  return path.startsWith("/") && !path.includes("//") && !path.startsWith("/@") &&
    !/[\\\u0000-\u001f]/.test(path) && !path.split("/").some(part => part === "." || part === "..")
}
export function newDerivativeRule() {
  return { name: "", path: "", recursive: true, profiles: ["thumb", "reader"] as ("thumb" | "reader")[],
    enabled: false, interval_hours: 6, excludes: [] as string[] }
}
