import type { DerivativeMedia, MediaSource } from "../../../utils/derivative-policy"
import {
  isSlowReaderConnection,
  LARGE_READER_IMAGE,
  readerImageTimeout,
  type ReaderConnection,
} from "../../reader/reader-loading-policy"

export interface LoadedImage {
  src: string
  width: number
  height: number
}

export interface ImageTask {
  promise: Promise<LoadedImage>
  cancel: () => void
}

export class ImageRequestError extends Error {
  readonly reason: "error" | "timeout" | "cancelled"
  constructor(reason: "error" | "timeout" | "cancelled") {
    super(`Image request ${reason}`)
    this.reason = reason
  }
}

/** A request owns its handlers and deadline, even if the browser never responds. */
export function requestImage(
  image: HTMLImageElement,
  source: MediaSource,
  connection?: ReaderConnection,
): ImageTask {
  let settled = false
  let resolve!: (value: LoadedImage) => void
  let reject!: (reason: ImageRequestError) => void
  const promise = new Promise<LoadedImage>((yes, no) => {
    resolve = yes
    reject = no
  })
  const finish = (reason?: ImageRequestError["reason"]) => {
    if (settled) return
    settled = true
    clearTimeout(timer)
    image.onload = null
    image.onerror = null
    if (reason) {
      image.removeAttribute("src")
      reject(new ImageRequestError(reason))
    } else {
      resolve({
        src: source.url,
        width: image.naturalWidth,
        height: image.naturalHeight,
      })
    }
  }
  image.onload = () => {
    const valid = [image.naturalWidth, image.naturalHeight].every(
      (size) => Number.isFinite(size) && size > 0,
    )
    finish(valid ? undefined : "error")
  }
  image.onerror = () => finish("error")
  image.decoding = "async"
  const timer = setTimeout(() => finish("timeout"), readerImageTimeout(source.bytes, connection))
  try {
    image.src = source.url
  } catch {
    finish("error")
  }
  return { promise, cancel: () => finish("cancelled") }
}

const activeMedia = new WeakMap<DerivativeMedia, Promise<LoadedImage>>()

/** Recover only failed previews through the permission policy, never retry a timeout. */
export function requestMedia(
  image: HTMLImageElement,
  media: DerivativeMedia,
  connection?: ReaderConnection,
): ImageTask {
  const controller = new AbortController()
  let task: ImageTask | undefined
  const previous = activeMedia.get(media)
  const promise = (async () => {
    // A cancelled status refresh must release its permission gate before a revisit.
    if (previous) await previous.catch(() => {})
    let source = media.source()
    while (source && !controller.signal.aborted) {
      task = requestImage(image, source, connection)
      try { return await task.promise }
      catch (error) {
        if (!(error instanceof ImageRequestError) || error.reason !== "error") throw error
        source = await media.recover(source.url, controller.signal)
      }
    }
    throw new ImageRequestError(controller.signal.aborted ? "cancelled" : "error")
  })()
  activeMedia.set(media, promise)
  const release = () => {
    if (activeMedia.get(media) === promise) activeMedia.delete(media)
  }
  void promise.then(release, release)
  return {
    promise,
    cancel() { controller.abort(); task?.cancel() },
  }
}

/** Metadata only: keep no decoded images, dedupe requests, release every lease. */
export function createImageRequests(
  makeImage: () => HTMLImageElement = () => new Image(),
) {
  const cache = new Map<string, LoadedImage>()
  const pending = new Map<string, ImageTask>()
  let disposed = false
  const cancel = (url: string) => {
    const task = pending.get(url)
    pending.delete(url)
    task?.cancel()
  }
  const load = (source: MediaSource, connection?: ReaderConnection): Promise<LoadedImage> => {
    const url = source.url
    if (disposed) return Promise.reject(new ImageRequestError("cancelled"))
    if (cache.has(url)) return Promise.resolve(cache.get(url)!)
    if (pending.has(url)) return pending.get(url)!.promise
    const image = makeImage()
    image.fetchPriority = "low"
    const task = requestImage(image, source, connection)
    pending.set(url, task)
    void task.promise.then(
      (data) => {
        if (!disposed && pending.get(url) === task) {
          cache.set(url, data)
          pending.delete(url)
        }
      },
      () => {
        // A late rejection must not erase a newer retry for the same URL.
        if (pending.get(url) === task) pending.delete(url)
      },
    )
    return task.promise
  }
  return {
    cache,
    pending,
    load,
    forget(url: string) {
      cancel(url)
      cache.delete(url)
    },
    retain(urls: Iterable<string>) {
      const keep = new Set(urls)
      for (const url of pending.keys()) if (!keep.has(url)) cancel(url)
    },
    dispose() {
      disposed = true
      for (const url of pending.keys()) cancel(url)
      cache.clear()
    },
  }
}

/** Only the next small preview may warm, and only after the visible image settles. */
export function neighbourIndices(
  index: number,
  list: (MediaSource | undefined)[],
  connection?: ReaderConnection,
  currentComplete = false,
) {
  if (!currentComplete || isSlowReaderConnection(connection)) return []
  const next = list[index + 1]
  return next?.kind === "preview" && next.bytes > 0 && next.bytes < LARGE_READER_IMAGE
    ? [index + 1]
    : []
}
