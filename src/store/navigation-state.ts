/** Pure navigation primitives. No credentials, DOM or browser storage. */
export const normalizeDirectory = (path: string) => {
  const result = `/${path.split("/").filter(Boolean).join("/")}`
  return result || "/"
}

export const directoryHref = (directory: string, page?: number) => {
  const path = normalizeDirectory(directory)
    .split("/")
    .map(encodeURIComponent)
    .join("/")
  const index = validPage(page)
  return index > 1 ? `${path}?page=${index}` : path
}

export const validPage = (value: unknown) => {
  const page = Number(value)
  return Number.isSafeInteger(page) && page > 0 ? page : 1
}

/** Decode the pathname only, never a query or an entire signed URL. */
export const parseDirectoryHref = (href: string, origin: string, base = "") => {
  try {
    const url = new URL(href, origin)
    if (url.origin !== new URL(origin).origin) return undefined
    const prefix = base.replace(/\/$/, "")
    if (
      prefix &&
      url.pathname !== prefix &&
      !url.pathname.startsWith(`${prefix}/`)
    )
      return undefined
    const path = normalizeDirectory(
      decodeURIComponent(url.pathname.slice(prefix.length)),
    )
    return { path, page: validPage(url.searchParams.get("page")) }
  } catch {
    return undefined
  }
}

export class BoundedHistory<T> {
  private entries = new Map<
    string,
    { value: T; time: number; weight: number }
  >()
  private weight = 0
  constructor(
    private maxEntries = 24,
    private maxWeight = 8_000_000,
    private ttl = 300_000,
  ) {}
  get size() {
    return this.entries.size
  }
  clear() {
    this.entries.clear()
    this.weight = 0
  }
  delete(key: string) {
    const entry = this.entries.get(key)
    if (entry) {
      this.weight -= entry.weight
      this.entries.delete(key)
    }
  }
  prune(now = Date.now()) {
    for (const [key, entry] of this.entries)
      if (now - entry.time > this.ttl) this.delete(key)
  }
  get(key: string, now = Date.now()) {
    this.prune(now)
    const entry = this.entries.get(key)
    if (!entry) return undefined
    this.entries.delete(key)
    this.entries.set(key, entry)
    return entry.value
  }
  values(now = Date.now()) {
    this.prune(now)
    return Array.from(this.entries.values(), (entry) => entry.value)
  }
  set(key: string, value: T, weight = 1, now = Date.now()) {
    this.prune(now)
    this.delete(key)
    if (weight > this.maxWeight) return false
    this.entries.set(key, { value, time: now, weight })
    this.weight += weight
    while (this.entries.size > this.maxEntries || this.weight > this.maxWeight)
      this.delete(this.entries.keys().next().value!)
    return true
  }
}

export type DirectoryErrorKind =
  "missing" | "permission" | "password" | "network" | "other"
export const classifyDirectoryError = (
  message = "",
  code?: number,
): DirectoryErrorKind => {
  if (/password|密码|密碼/i.test(message) && code === 403) return "password"
  if (
    code === 401 ||
    code === 403 ||
    /permission denied|access denied|forbidden/i.test(message)
  )
    return "permission"
  if (
    code === 404 ||
    /object not found|file not found|no such file|path not found/i.test(message)
  )
    return "missing"
  if (
    code === undefined ||
    code === 408 ||
    code === 429 ||
    code === 502 ||
    code === 503 ||
    code === 504 ||
    /network|timeout|timed out|failed to fetch|connection/i.test(message)
  )
    return "network"
  return "other"
}

/** Never echo arbitrary server text: it can contain private paths or signed URLs. */
export const directoryErrorDetails = (
  kind: DirectoryErrorKind,
  code?: number,
) => `${kind}${code === undefined ? "" : ` (HTTP/API ${code})`}`

export const mergeDirectoryPage = <T extends { name: string }>(
  old: T[],
  incoming: T[],
) => {
  const names = new Set(old.map((item) => item.name))
  return [
    ...old,
    ...incoming.filter(
      (item) => !names.has(item.name) && !!names.add(item.name),
    ),
  ]
}

/** A failed/obsolete page is terminal for this run; total can shrink at any page. */
export const collectDirectoryPages = async <T>(options: {
  target: number
  size: number
  isCurrent: () => boolean
  fetch: (page: number) => Promise<{ items: T[]; total: number } | undefined>
}) => {
  let items: T[] = []
  let total = 0
  let page = 0
  const target = validPage(options.target)
  while (page < target && options.isCurrent()) {
    const next = await options.fetch(page + 1)
    if (!next || !options.isCurrent())
      return { ok: false as const, items, total, page }
    items = items.concat(next.items)
    total = Math.max(0, next.total)
    page++
    if (
      !next.items.length ||
      options.size <= 0 ||
      page >= Math.ceil(total / options.size)
    )
      break
  }
  return { ok: options.isCurrent(), items, total, page }
}

export class NavigationEpoch {
  private value = 0
  begin() {
    return ++this.value
  }
  current() {
    return this.value
  }
  matches(value: number) {
    return this.value === value
  }
}
