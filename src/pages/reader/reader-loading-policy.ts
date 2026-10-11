// Pure scheduling rules shared by the reader and regression tests. Keep original
// <img> requests (and browser caching); do not buffer entire originals in JS.
export interface ReaderConnection {
  saveData?: boolean
  effectiveType?: string
  downlink?: number
}

export interface ReaderLoadCandidate {
  index: number
  state: "queued" | "loading"
  visible: boolean
  distance: number
  ahead: boolean
  size: number
}

export const LARGE_READER_IMAGE = 2 * 1024 * 1024

export function isSlowReaderConnection(connection?: ReaderConnection): boolean {
  return Boolean(
    connection?.saveData ||
    ["slow-2g", "2g", "3g"].includes(connection?.effectiveType ?? "") ||
    (Number.isFinite(connection?.downlink) &&
      connection!.downlink! > 0 &&
      connection!.downlink! <= 5),
  )
}

// Native <img> does not expose transfer progress across origins. Use a bounded
// size/network budget instead of pretending a 30s timer detects a stalled stream.
export function readerImageTimeout(
  size: number,
  connection?: ReaderConnection,
): number {
  const bytes = Number.isFinite(size) ? Math.max(0, size) : 0
  const downlink = connection?.downlink
  const hintedBytesPerSecond =
    downlink && Number.isFinite(downlink) && downlink > 0
      ? (downlink * 1_000_000) / 8
      : 256 * 1024
  // Honor explicitly slow positive hints; the deadline cap bounds waiting.
  const budgetRate = Math.min(256 * 1024, hintedBytesPerSecond)
  return Math.min(
    180_000,
    Math.max(30_000, Math.ceil(15_000 + (bytes / budgetRate) * 1_500)),
  )
}

export function planReaderLoads(
  candidates: ReaderLoadCandidate[],
  pendingIndex: number | undefined,
  connection?: ReaderConnection,
): { start: number[]; cancel: number[]; highPriority: number[] } {
  const pending = candidates.find((item) => item.index === pendingIndex)
  const visible = candidates.filter((item) => item.visible)
  const urgent = pending ? [pending] : visible
  const slow = isSlowReaderConnection(connection)
  let selected: ReaderLoadCandidate[]
  if (urgent.length) {
    // A jump is exclusive. Large visible originals must not compete with
    // speculative downloads; retain an already-visible download to avoid churn.
    const limit =
      pending || slow || urgent.some((item) => item.size >= LARGE_READER_IMAGE)
        ? 1
        : 2
    selected = [...urgent]
      .sort(
        (a, b) =>
          Number(b.state === "loading") - Number(a.state === "loading") ||
          a.distance - b.distance ||
          a.index - b.index,
      )
      .slice(0, limit)
  } else {
    // No whole-directory prefetch. One nearby page on normal connections, none
    // on slow/metered connections. Keep an existing prefetch until interrupted.
    selected = slow
      ? []
      : [...candidates]
          .sort(
            (a, b) =>
              Number(b.state === "loading") - Number(a.state === "loading") ||
              Number(b.ahead) - Number(a.ahead) ||
              a.distance - b.distance ||
              a.index - b.index,
          )
          .slice(0, 1)
  }
  const wanted = new Set(selected.map((item) => item.index))
  return {
    start: selected
      .filter((item) => item.state === "queued")
      .map((item) => item.index),
    cancel: candidates
      .filter((item) => item.state === "loading" && !wanted.has(item.index))
      .map((item) => item.index),
    highPriority: selected
      .filter((item) => item.visible || item.index === pendingIndex)
      .map((item) => item.index),
  }
}
