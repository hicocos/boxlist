export interface LoadedImage {
  src: string
  width: number
  height: number
}

export const IMAGE_TIMEOUT = 30_000

export class ImageRequestError extends Error {
  constructor(readonly reason: "error" | "timeout" | "cancelled") {
    super(`Image request ${reason}`)
  }
}

/** A request owns its handlers and deadline, even if the browser never responds. */
export function requestImage(
  image: HTMLImageElement,
  url: string,
  timeout = IMAGE_TIMEOUT,
) {
  let settled = false
  let timer: ReturnType<typeof setTimeout> | undefined
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
        src: url,
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
  timer = setTimeout(() => finish("timeout"), timeout)
  try {
    image.src = url
  } catch {
    finish("error")
  }
  return { promise, cancel: () => finish("cancelled") }
}

/** Metadata only: keep no decoded images, dedupe requests, release every lease. */
export function createImageRequests(
  makeImage: () => HTMLImageElement = () => new Image(),
  timeout = IMAGE_TIMEOUT,
) {
  const cache = new Map<string, LoadedImage>()
  const pending = new Map<string, ReturnType<typeof requestImage>>()
  let disposed = false
  const cancel = (url: string) => {
    const task = pending.get(url)
    pending.delete(url)
    task?.cancel()
  }
  const load = (url: string): Promise<LoadedImage> => {
    if (disposed) return Promise.reject(new ImageRequestError("cancelled"))
    if (cache.has(url)) return Promise.resolve(cache.get(url)!)
    if (pending.has(url)) return pending.get(url)!.promise
    const task = requestImage(makeImage(), url, timeout)
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

export function neighbourIndices(
  index: number,
  list: { size: number }[],
  connection?: { saveData?: boolean; effectiveType?: string },
) {
  if (connection?.saveData || /(^|-)2g$/.test(connection?.effectiveType || ""))
    return []
  return [index - 1, index + 1].filter(
    (i) => list[i] && list[i].size <= 20 * 1024 * 1024,
  )
}
