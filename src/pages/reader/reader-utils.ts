import type { Obj } from "~/types"

export const isReaderImage = (obj: Pick<Obj, "name" | "is_dir">) =>
  !obj.is_dir && /\.(jpe?g|jfif|png|gif|webp|svg|avif|bmp|ico)$/i.test(obj.name)

export interface ReaderImage {
  key: string
  src: string
  name: string
  size: number
}
export interface ReadingProgress {
  name: string
  fraction: number
  mode: "continuous" | "single"
  reverse: boolean
  gap: number
  time: number
}

export const validDirectory = (value: unknown) =>
  typeof value === "string" &&
  value.startsWith("/") &&
  !value.startsWith("//") &&
  !/[\u0000-\u001f]/.test(value) &&
  !value.split("/").some((part) => part === "." || part === "..") &&
  !value.startsWith("/@")

const progressKey = "openlist-reader-progress-v1"
export function readProgress(key: string): ReadingProgress | undefined {
  try {
    const value = JSON.parse(localStorage.getItem(progressKey) || "{}")[key]
    if (
      value &&
      typeof value.name === "string" &&
      Number.isFinite(value.fraction)
    )
      return {
        ...value,
        fraction: Math.max(0, Math.min(1, value.fraction)),
        mode: value.mode === "single" ? "single" : "continuous",
        reverse: value.reverse === true,
        gap: value.gap === 0 ? 0 : 4,
      }
  } catch {
    /* Private browsing or corrupt local state must not block reading. */
  }
}
export function writeProgress(key: string, value: ReadingProgress) {
  try {
    const raw = JSON.parse(localStorage.getItem(progressKey) || "{}")
    const data: Record<string, ReadingProgress> =
      raw && typeof raw === "object" && !Array.isArray(raw) ? raw : {}
    data[key] = value
    const kept = Object.entries(data)
      .sort((a, b) => (b[1]?.time || 0) - (a[1]?.time || 0))
      .slice(0, 40)
    localStorage.setItem(progressKey, JSON.stringify(Object.fromEntries(kept)))
  } catch {
    /* Progress is optional; never save image URLs or passwords. */
  }
}
