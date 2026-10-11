import { r } from "./request"
import type { Resp } from "~/types/resp"
import type { Derivatives } from "~/types/derivatives"

export async function derivativeAPI<T>(path: string, body?: unknown, signal?: AbortSignal, timeout = 15000): Promise<T> {
  const config = { signal, timeout }
  const response = (body === undefined
    ? await r.get(`/admin/derivatives${path}`, config)
    : await r.post(`/admin/derivatives${path}`, body, config)) as unknown as Resp<T>
  if (response.code !== 200) throw new Error(response.message || String(response.code || "Network error"))
  return response.data
}
export async function deleteDerivativeRule(id: string | number, signal?: AbortSignal): Promise<void> {
  const response = await r.delete(`/admin/derivatives/rules/${encodeURIComponent(id)}`, { signal, timeout: 15000 }) as unknown as Resp<unknown>
  if (response.code !== 200) throw new Error(response.message || String(response.code || "Network error"))
}
/** Diagnose a failed renewed ticket; cancel the body immediately, never preload it. */
export async function probeDerivative(url: string, signal?: AbortSignal) {
  const controller = new AbortController()
  const abort = () => controller.abort()
  if (signal?.aborted) return 0
  signal?.addEventListener("abort", abort, { once: true })
  const timer = setTimeout(abort, 10000)
  try {
    const response = await fetch(url, { signal: controller.signal, headers: { Range: "bytes=0-0" }, cache: "no-store", credentials: "same-origin" })
    void response.body?.cancel().catch(() => {})
    return response.status
  } catch { return 0 }
  finally { clearTimeout(timer); signal?.removeEventListener("abort", abort) }
}
export async function derivativeStatus(path: string, password: string, names: string[], signal?: AbortSignal) {
  return await r.post("/fs/derivatives/status", { path, password, names }, { signal, timeout: 15000 }) as unknown as Resp<Record<string, Derivatives>>
}

/** A scope-owned, serial, bounded batcher. Never cache permission decisions/tickets. */
export function createDerivativeStatusBatch(path: string, password: string) {
  type Entry = { name: string; signal?: AbortSignal; resolve: (value: { code: number; derivatives?: Derivatives }) => void }
  let queue: Entry[] = []
  let running = false
  let disposed = false
  const controller = new AbortController()
  const drain = async () => {
    if (running || disposed) return
    running = true
    try {
      while (queue.length && !disposed) {
        const entries = queue.splice(0, 64)
        const names = [...new Set(entries.filter(e => !e.signal?.aborted).map(e => e.name))]
        let result: { code: number; data?: Record<string, Derivatives> } = { code: -1 }
        try { if (names.length) result = await derivativeStatus(path, password, names, controller.signal) } catch { result = { code: 0 } }
        for (const entry of entries) entry.resolve({ code: disposed || entry.signal?.aborted ? -1 : result.code, derivatives: result.data?.[entry.name] })
      }
    } finally { running = false }
  }
  return {
    refresh(name: string, signal?: AbortSignal): Promise<{ code: number; derivatives?: Derivatives }> {
      if (disposed || signal?.aborted) return Promise.resolve({ code: -1 })
      return new Promise(resolve => { queue.push({ name, signal, resolve }); queueMicrotask(drain) })
    },
    dispose() {
      disposed = true
      controller.abort()
      for (const entry of queue.splice(0)) entry.resolve({ code: -1 })
    },
  }
}
