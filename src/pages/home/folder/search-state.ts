/** Search-only state. Nothing in this module writes browser storage. */
export const SEARCH_PAGE_SIZE = 100
export const SEARCH_TIMEOUT_MS = 30_000
export type SearchScope = 0 | 1 | 2
export type SearchDraft = {
  keywords: string
  scope: SearchScope
}
export type SearchQuery = Readonly<{
  keywords: string
  scope: SearchScope
  parent: string
}>
export type SearchEntry = {
  name: string
  parent: string
  path: string
  is_dir: boolean
  size: number
  type: number
}
export type SearchPage = {
  content: SearchEntry[]
  total: number
}
export type SearchResultPage = SearchPage & {
  query: SearchQuery
  page: number
}
export type SearchError =
  "network" | "access" | "server" | "invalid" | "timeout"
export type SearchState = {
  draft: SearchDraft
  submitted: SearchQuery | null
  requestedPage: number
  result: SearchResultPage | null
  status: "idle" | "loading" | "success" | "error" | "cancelled"
  error: SearchError | null
  open: boolean
  scrollTop: number
  focusPath: string | null
}

export const emptySearchState = (): SearchState => ({
  draft: { keywords: "", scope: 0 },
  submitted: null,
  requestedPage: 1,
  result: null,
  status: "idle",
  error: null,
  open: false,
  scrollTop: 0,
  focusPath: null,
})

export const sameQuery = (a: SearchQuery | null, b: SearchQuery | null) =>
  a === b ||
  (!!a &&
    !!b &&
    a.keywords === b.keywords &&
    a.scope === b.scope &&
    a.parent === b.parent)

export const queryFromDraft = (
  draft: SearchDraft,
  _directory?: string,
): SearchQuery =>
  Object.freeze({
    keywords: draft.keywords.trim(),
    scope: draft.scope,
    // The server joins this virtual root to the authenticated user's base_path.
    parent: "/",
  })

export const canSubmitSearchKey = (
  event: { key: string; isComposing?: boolean; keyCode?: number },
  composing: boolean,
) =>
  event.key === "Enter" &&
  !composing &&
  !event.isComposing &&
  event.keyCode !== 229

export const canCloseSearchKey = (
  event: {
    key: string
    isComposing?: boolean
    keyCode?: number
    defaultPrevented?: boolean
  },
  menuOpen: boolean,
) =>
  event.key === "Escape" &&
  !menuOpen &&
  !event.isComposing &&
  event.keyCode !== 229 &&
  !event.defaultPrevented

export class SearchFailure extends Error {
  kind: SearchError
  constructor(kind: SearchError) {
    super(kind)
    this.kind = kind
  }
}

/** Strip a literal, segment-aligned base path, never a regular expression. */
export const relativeSearchParent = (parent: string, basePath: string) => {
  const base = basePath.replace(/\/+$/, "")
  if (
    !parent.startsWith("/") ||
    parent.split("/").some((part) => part === "." || part === "..")
  ) {
    throw new SearchFailure("invalid")
  }
  if (!base) return parent
  if (parent === base) return "/"
  if (parent.startsWith(`${base}/`)) return parent.slice(base.length)
  throw new SearchFailure("invalid")
}

/** Whitelist metadata and derive local routes; discard any server URL/sign/password. */
export const decodeSearchPage = (
  value: unknown,
  basePath: string,
): SearchPage => {
  const response = value as {
    code?: number
    data?: { total?: number; content?: unknown[] | null }
  }
  if (response?.code !== 200) {
    throw new SearchFailure(
      response?.code === 401 || response?.code === 403
        ? "access"
        : response?.code === undefined || response?.code === -1
          ? "network"
          : "server",
    )
  }
  const total = response.data?.total
  const raw = response.data?.content ?? (total === 0 ? [] : undefined)
  if (
    !Number.isSafeInteger(total) ||
    total! < 0 ||
    !Array.isArray(raw) ||
    raw.length > SEARCH_PAGE_SIZE ||
    raw.length > total!
  ) {
    throw new SearchFailure("invalid")
  }
  const content = raw.map((entry) => {
    const item = entry as Partial<SearchEntry>
    if (
      !item ||
      typeof item.parent !== "string" ||
      typeof item.name !== "string" ||
      !item.name ||
      item.name.includes("/") ||
      item.name === "." ||
      item.name === ".." ||
      typeof item.is_dir !== "boolean"
    ) {
      throw new SearchFailure("invalid")
    }
    const parent = relativeSearchParent(item.parent, basePath)
    const path = `${parent.replace(/\/+$/, "")}/${item.name}`
      .split("/")
      .map(encodeURIComponent)
      .join("/")
    return {
      name: item.name,
      parent,
      path,
      is_dir: item.is_dir,
      size:
        typeof item.size === "number" && Number.isFinite(item.size)
          ? Math.max(0, item.size)
          : 0,
      type: typeof item.type === "number" ? item.type : item.is_dir ? 1 : 0,
    }
  })
  return { total: total!, content }
}

export type SearchTransport = (
  query: SearchQuery,
  page: number,
  signal: AbortSignal,
) => Promise<SearchPage>

export const createSearchController = (
  initial: SearchState,
  transport: SearchTransport,
  changed: (state: SearchState) => void,
  timeoutMs = SEARCH_TIMEOUT_MS,
) => {
  let state = initial
  let generation = 0
  let disposed = false
  let active: { abort: AbortController; stop: () => void } | undefined
  const update = (patch: Partial<SearchState>) => {
    state = { ...state, ...patch }
    changed(state)
  }
  const stop = () => {
    generation++
    active?.stop()
    active?.abort.abort()
    active = undefined
  }
  const run = async (query: SearchQuery, page: number) => {
    if (
      disposed ||
      !query.keywords ||
      query.keywords.length > 512 ||
      !Number.isSafeInteger(page) ||
      page < 1
    )
      return
    stop()
    const ticket = generation
    const abort = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let release!: () => void
    const interrupted = new Promise<never>((_, reject) => {
      release = () => {
        clearTimeout(timer)
        reject(new SearchFailure("network"))
      }
      timer = setTimeout(() => {
        reject(new SearchFailure("timeout"))
        abort.abort()
      }, timeoutMs)
    })
    active = { abort, stop: release }
    const committed = Object.freeze({ ...query, parent: "/" })
    update({
      submitted: committed,
      requestedPage: page,
      status: "loading",
      error: null,
    })
    try {
      const data = await Promise.race([
        transport(committed, page, abort.signal),
        interrupted,
      ])
      if (disposed || ticket !== generation) return
      update({
        result: { ...data, query: committed, page },
        status: "success",
        error: null,
        scrollTop: 0,
        focusPath: null,
      })
    } catch (error) {
      if (disposed || ticket !== generation) return
      update({
        status: "error",
        error: error instanceof SearchFailure ? error.kind : "network",
      })
    } finally {
      clearTimeout(timer)
      if (ticket === generation) active = undefined
    }
  }
  return {
    getState: () => state,
    setDraft: (draft: Partial<SearchDraft>) =>
      update({ draft: { ...state.draft, ...draft } }),
    setView: (
      view: Pick<Partial<SearchState>, "open" | "scrollTop" | "focusPath">,
    ) => update(view),
    submit: (directory: string) =>
      run(queryFromDraft(state.draft, directory), 1),
    // Pagination belongs to the displayed committed results, never the draft.
    page: (page: number) => {
      const result = state.result
      if (
        !result ||
        page > Math.max(1, Math.ceil(result.total / SEARCH_PAGE_SIZE))
      )
        return Promise.resolve()
      return run(result.query, page)
    },
    retry: () =>
      state.submitted
        ? run(state.submitted, state.requestedPage)
        : Promise.resolve(),
    cancel: () => {
      stop()
      if (state.status === "loading")
        update({ status: "cancelled", error: null })
    },
    dispose: () => {
      stop()
      if (state.status === "loading")
        update({ status: "cancelled", error: null })
      disposed = true
    },
  }
}

export const searchAccountKey = (user: {
  id?: number
  username?: string
  base_path?: string
  role?: number
  permission?: number
}) =>
  JSON.stringify([
    user.id ?? null,
    user.username ?? "",
    user.base_path ?? "",
    user.role ?? null,
    user.permission ?? null,
  ])

/** Bounded, expiring, single-account memory only. A new identity purges the old one. */
export const createSearchSessionCache = (
  options: {
    maxEntries?: number
    maxBytes?: number
    ttlMs?: number
    now?: () => number
  } = {},
) => {
  const maxEntries = options.maxEntries ?? 8
  const maxBytes = options.maxBytes ?? 2 * 1024 * 1024
  const ttlMs = options.ttlMs ?? 30 * 60 * 1000
  const now = options.now ?? Date.now
  const entries = new Map<
    string,
    { state: SearchState; at: number; bytes: number }
  >()
  let owner = ""
  const account = (key: string) => {
    if (owner !== key) entries.clear()
    owner = key
  }
  const prune = () => {
    for (const [key, value] of entries)
      if (now() - value.at > ttlMs) entries.delete(key)
    let bytes = [...entries.values()].reduce(
      (sum, entry) => sum + entry.bytes,
      0,
    )
    while (entries.size > maxEntries || bytes > maxBytes) {
      const key = entries.keys().next().value!
      bytes -= entries.get(key)!.bytes
      entries.delete(key)
    }
  }
  return {
    account,
    get: (identity: string, directory: string): SearchState | undefined => {
      account(identity)
      prune()
      const entry = entries.get(directory)
      if (!entry) return
      entries.delete(directory)
      entries.set(directory, entry)
      return structuredClone(entry.state)
    },
    put: (identity: string, directory: string, state: SearchState) => {
      // Stale requests/cleanup from a previous account may not reclaim ownership.
      if (owner !== identity) return
      const snapshot = structuredClone(state)
      if (snapshot.status === "loading") snapshot.status = "cancelled"
      const bytes = JSON.stringify(snapshot).length * 2 + directory.length * 2
      entries.delete(directory)
      if (bytes <= maxBytes)
        entries.set(directory, { state: snapshot, at: now(), bytes })
      prune()
    },
    size: () => {
      prune()
      return entries.size
    },
  }
}

export const searchSessions = createSearchSessionCache()
