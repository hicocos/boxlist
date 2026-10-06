import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"
import ts from "typescript"

// Transpile only the dependency-free search module; no browser or backend fixtures.
const source = await readFile(
  new URL("../src/pages/home/folder/search-state.ts", import.meta.url),
  "utf8",
)
const code = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.ESNext,
    target: ts.ScriptTarget.ES2022,
  },
}).outputText
const {
  canCloseSearchKey,
  canSubmitSearchKey,
  createSearchController,
  createSearchSessionCache,
  decodeSearchPage,
  emptySearchState,
  queryFromDraft,
  relativeSearchParent,
  sameQuery,
  searchAccountKey,
  SearchFailure,
} = await import(
  `data:text/javascript;base64,${Buffer.from(code).toString("base64")}`
)

const deferred = () => {
  let resolve, reject
  const promise = new Promise((a, b) => {
    resolve = a
    reject = b
  })
  return { promise, resolve, reject }
}
const pageData = (name = "result", total = 301) => ({
  content: [
    { name, parent: "/", path: `/${name}`, type: 0, size: 1, is_dir: false },
  ],
  total,
})
const harness = (timeout = 5000) => {
  const requests = []
  const states = []
  const controller = createSearchController(
    emptySearchState(),
    (query, page, signal) => {
      const pending = deferred()
      requests.push({ ...pending, query, page, signal })
      return pending.promise
    },
    (state) => states.push(state),
    timeout,
  )
  return { controller, requests, states }
}

test("search always uses virtual root, including legacy root=false and nested directories", () => {
  const state = emptySearchState()
  assert.equal(state.status, "idle")
  assert.equal(state.result, null)
  assert.equal(state.open, false)
  assert.deepEqual(
    queryFromDraft(
      { keywords: "  hello  ", scope: 2, root: false },
      "/current",
    ),
    { keywords: "hello", scope: 2, parent: "/" },
  )
  assert.equal(
    queryFromDraft({ keywords: "hello", scope: 1, root: true }, "/private")
      .parent,
    "/",
  )
})

test("restored non-root queries cannot change forced-root pagination or retry", async () => {
  const requests = []
  const initial = {
    ...emptySearchState(),
    submitted: { keywords: "old", scope: 1, parent: "/old-directory" },
    requestedPage: 2,
    result: {
      ...pageData(),
      query: { keywords: "old", scope: 1, parent: "/old-directory" },
      page: 1,
    },
  }
  const controller = createSearchController(
    initial,
    async (query, page) => {
      requests.push({ query, page })
      return pageData()
    },
    () => {},
  )
  await controller.retry()
  await controller.page(3)
  assert.deepEqual(
    requests.map((x) => x.query.parent),
    ["/", "/"],
  )
  assert.deepEqual(
    requests.map((x) => x.page),
    [2, 3],
  )
})

test("compact search view removes directory controls and preserves old numeric paginator", async () => {
  const view = await readFile(
    new URL("../src/pages/home/folder/Search.tsx", import.meta.url),
    "utf8",
  )
  assert.doesNotMatch(
    view,
    /search-location|search-root-control|search-scope-help|type="checkbox"|扩大到根目录|当前目录：|search-keyword-label/,
  )
  assert.match(view, /parent: "\/"/)
  assert.match(view, /<Paginator/)
  assert.match(view, /current=\{state\(\)\.result\?\.page \?\? 1\}/)
  assert.match(view, /canSubmitSearchKey/)
  assert.match(view, /controller\.retry\(\)/)
})

test("blank and too-long searches do not send a request", async () => {
  const { controller, requests } = harness()
  controller.setDraft({ keywords: " \n " })
  await controller.submit("/")
  controller.setDraft({ keywords: "a".repeat(513) })
  await controller.submit("/")
  assert.equal(requests.length, 0)
  assert.equal(controller.getState().status, "idle")
})

test("submitted snapshot is immutable; all pagination uses displayed query, not draft", async () => {
  const { controller, requests } = harness()
  controller.setDraft({ keywords: "first", scope: 1 })
  const initial = controller.submit("/original")
  assert.equal(controller.getState().status, "loading")
  requests[0].resolve(pageData())
  await initial
  controller.setDraft({ keywords: "second", scope: 2, root: true })
  const next = controller.page(3)
  assert.deepEqual(requests[1].query, {
    keywords: "first",
    scope: 1,
    parent: "/",
  })
  assert.equal(requests[1].page, 3)
  assert.equal(Object.isFrozen(requests[1].query), true)
  assert.equal(controller.getState().result.page, 1)
  requests[1].resolve(pageData("page3"))
  await next
  assert.equal(controller.getState().result.page, 3)
  assert.equal(controller.getState().draft.keywords, "second")
  const before = requests.length
  await controller.page(0)
  await controller.page(100)
  assert.equal(requests.length, before)
})

test("a new submission aborts/replaces an old request and stale success cannot win", async () => {
  const { controller, requests } = harness()
  controller.setDraft({ keywords: "old" })
  const old = controller.submit("/")
  controller.setDraft({ keywords: "new" })
  const current = controller.submit("/")
  assert.equal(requests[0].signal.aborted, true)
  requests[1].resolve(pageData("new"))
  await current
  requests[0].resolve(pageData("old"))
  await old
  assert.equal(controller.getState().result.content[0].name, "new")
  assert.equal(controller.getState().submitted.keywords, "new")
  assert.equal(controller.getState().status, "success")
})

test("stale failure/finally cannot clear newer loading or error state", async () => {
  const { controller, requests } = harness()
  controller.setDraft({ keywords: "old" })
  const old = controller.submit("/")
  controller.setDraft({ keywords: "new" })
  const current = controller.submit("/")
  requests[0].reject(new Error("offline"))
  await old
  assert.equal(controller.getState().status, "loading")
  requests[1].reject(new SearchFailure("access"))
  await current
  assert.equal(controller.getState().error, "access")
})

test("old results stay visible during replacement and failure; retry keeps failed snapshot/page", async () => {
  const { controller, requests } = harness()
  controller.setDraft({ keywords: "good" })
  const first = controller.submit("/here")
  requests[0].resolve(pageData("good"))
  await first
  const oldResult = controller.getState().result
  controller.setDraft({ keywords: "broken", scope: 2, root: true })
  const broken = controller.submit("/here")
  assert.equal(controller.getState().result, oldResult)
  requests[1].reject(new Error("network"))
  await broken
  assert.equal(controller.getState().result, oldResult)
  assert.equal(controller.getState().status, "error")
  controller.setDraft({ keywords: "unsubmitted" })
  const retry = controller.retry()
  assert.deepEqual(requests[2].query, {
    keywords: "broken",
    scope: 2,
    parent: "/",
  })
  assert.equal(requests[2].page, 1)
  requests[2].resolve({ total: 0, content: [] })
  await retry
  assert.equal(controller.getState().status, "success")
  assert.deepEqual(controller.getState().result.content, [])
  assert.equal(controller.getState().result.total, 0)
})

test("failed page retry is the requested page, not the retained result page", async () => {
  const { controller, requests } = harness()
  controller.setDraft({ keywords: "x" })
  const first = controller.submit("/")
  requests[0].resolve(pageData())
  await first
  const next = controller.page(2)
  requests[1].reject(new Error("offline"))
  await next
  assert.equal(controller.getState().result.page, 1)
  const retry = controller.retry()
  assert.equal(requests[2].page, 2)
  requests[2].resolve(pageData("next"))
  await retry
  assert.equal(controller.getState().result.page, 2)
})

test("cancel settles even an uncooperative transport and ignores its later result", async () => {
  const { controller, requests } = harness()
  controller.setDraft({ keywords: "x" })
  const pending = controller.submit("/")
  controller.cancel()
  await pending
  assert.equal(requests[0].signal.aborted, true)
  assert.equal(controller.getState().status, "cancelled")
  requests[0].resolve(pageData())
  await Promise.resolve()
  assert.equal(controller.getState().result, null)
})

test("timeout releases loading with a recoverable error even if transport never settles", async () => {
  const { controller, requests } = harness(5)
  controller.setDraft({ keywords: "x" })
  await controller.submit("/")
  assert.equal(requests[0].signal.aborted, true)
  assert.equal(controller.getState().status, "error")
  assert.equal(controller.getState().error, "timeout")
  const retry = controller.retry()
  requests[1].resolve(pageData())
  await retry
  assert.equal(controller.getState().status, "success")
})

test("dispose aborts work and cannot publish late results or send new requests", async () => {
  const { controller, requests, states } = harness()
  controller.setDraft({ keywords: "x" })
  const pending = controller.submit("/")
  controller.dispose()
  const count = states.length
  requests[0].resolve(pageData())
  await pending
  await controller.submit("/")
  assert.equal(states.length, count)
  assert.equal(requests.length, 1)
  assert.equal(controller.getState().result, null)
})

test("successful replacement resets result page, scroll and focused row only on success", async () => {
  const { controller, requests } = harness()
  controller.setDraft({ keywords: "x" })
  controller.setView({ scrollTop: 245, focusPath: "/old", open: true })
  const request = controller.submit("/")
  assert.equal(controller.getState().scrollTop, 245)
  requests[0].resolve(pageData())
  await request
  assert.equal(controller.getState().scrollTop, 0)
  assert.equal(controller.getState().focusPath, null)
  assert.equal(controller.getState().result.page, 1)
})

test("base paths are stripped literally and segment boundaries cannot escape account root", () => {
  assert.equal(
    relativeSearchParent("/users/a.[b]/folder", "/users/a.[b]"),
    "/folder",
  )
  assert.equal(relativeSearchParent("/users/a.[b]", "/users/a.[b]/"), "/")
  assert.throws(
    () => relativeSearchParent("/users/a.[b]-other", "/users/a.[b]"),
    /invalid/,
  )
  assert.throws(
    () => relativeSearchParent("/users/a/../b", "/users/a"),
    /invalid/,
  )
  assert.throws(
    () => relativeSearchParent("/users/aXb", "/users/a.b"),
    /invalid/,
  )
})

test("response whitelist never caches passwords, signed URLs or server-provided paths", () => {
  const input = {
    code: 200,
    data: {
      total: 1,
      content: [
        {
          name: "a?# %.txt",
          parent: "/private",
          is_dir: false,
          size: 10,
          type: 4,
          password: "synthetic-secret",
          sign: "synthetic-sign",
          raw_url: "https://example.invalid/signed",
          path: "https://example.invalid/evil",
        },
      ],
    },
  }
  const output = decodeSearchPage(input, "/private")
  assert.equal(output.content[0].path, "/a%3F%23%20%25.txt")
  assert.deepEqual(
    Object.keys(output.content[0]).sort(),
    ["name", "parent", "path", "is_dir", "size", "type"].sort(),
  )
  assert.equal(JSON.stringify(output).includes("synthetic-secret"), false)
  assert.equal(
    input.data.content[0].parent,
    "/private",
    "response is not mutated",
  )
  assert.deepEqual(
    decodeSearchPage({ code: 200, data: { total: 0, content: null } }, "/"),
    { total: 0, content: [] },
  )
})

test("invalid counts, malformed content and access failures are not reported as no results", () => {
  for (const data of [
    { total: -1, content: [] },
    { total: 2 },
    { total: 0, content: [{}] },
    { total: 1, content: [{}] },
    { total: 101, content: Array(101).fill({}) },
  ]) {
    assert.throws(() => decodeSearchPage({ code: 200, data }, "/"), /invalid/)
  }
  assert.throws(() => decodeSearchPage({ code: 403 }, "/"), /access/)
  assert.throws(() => decodeSearchPage({ message: "offline" }, "/"), /network/)
  assert.throws(() => decodeSearchPage({ code: 500 }, "/"), /server/)
})

test("memory session restores open state, page, results, draft, scroll and focus", () => {
  const cache = createSearchSessionCache()
  cache.account("alice")
  const state = {
    ...emptySearchState(),
    open: true,
    status: "success",
    draft: { keywords: "draft", scope: 2, root: true },
    result: {
      ...pageData(),
      query: { keywords: "committed", scope: 0, parent: "/" },
      page: 3,
    },
    scrollTop: 900,
    focusPath: "/result",
  }
  cache.put("alice", "/origin", state)
  const restored = cache.get("alice", "/origin")
  assert.deepEqual(restored, state)
  restored.draft.keywords = "changed"
  assert.equal(cache.get("alice", "/origin").draft.keywords, "draft")
  assert.equal(cache.get("alice", "/other"), undefined)
})

test("memory cache has LRU entry, byte and TTL limits", () => {
  let now = 0
  const cache = createSearchSessionCache({
    maxEntries: 2,
    maxBytes: 4000,
    ttlMs: 10,
    now: () => now,
  })
  cache.account("alice")
  cache.put("alice", "/one", emptySearchState())
  cache.put("alice", "/two", emptySearchState())
  cache.get("alice", "/one")
  cache.put("alice", "/three", emptySearchState())
  assert.equal(cache.size(), 2)
  assert.equal(cache.get("alice", "/two"), undefined)
  const large = emptySearchState()
  large.draft.keywords = "x".repeat(5000)
  cache.put("alice", "/large", large)
  assert.equal(cache.get("alice", "/large"), undefined)
  now = 11
  assert.equal(cache.size(), 0)
})

test("switching accounts purges prior sessions and rejects stale-owner writes", () => {
  const cache = createSearchSessionCache()
  cache.account("alice")
  cache.put("alice", "/", { ...emptySearchState(), open: true })
  assert.equal(cache.get("bob", "/"), undefined)
  cache.put("alice", "/", emptySearchState())
  assert.equal(cache.get("bob", "/"), undefined)
  assert.equal(cache.get("alice", "/"), undefined)
  assert.notEqual(
    searchAccountKey({ id: 1, base_path: "/a" }),
    searchAccountKey({ id: 1, base_path: "/b" }),
  )
  assert.notEqual(
    searchAccountKey({ id: 1, permission: 1 }),
    searchAccountKey({ id: 1, permission: 2 }),
  )
})

test("restoring an interrupted request never restores a permanent loading spinner", () => {
  const cache = createSearchSessionCache()
  cache.account("alice")
  cache.put("alice", "/", { ...emptySearchState(), status: "loading" })
  assert.equal(cache.get("alice", "/").status, "cancelled")
})

test("IME Enter is ignored, ordinary Enter submits, and menu Escape does not close modal", () => {
  assert.equal(canSubmitSearchKey({ key: "Enter" }, false), true)
  assert.equal(
    canSubmitSearchKey({ key: "Enter", isComposing: true }, false),
    false,
  )
  assert.equal(canSubmitSearchKey({ key: "Enter", keyCode: 229 }, false), false)
  assert.equal(canSubmitSearchKey({ key: "Enter" }, true), false)
  assert.equal(canSubmitSearchKey({ key: "a" }, false), false)
  assert.equal(canCloseSearchKey({ key: "Escape" }, true), false)
  assert.equal(canCloseSearchKey({ key: "Escape" }, false), true)
  assert.equal(
    canCloseSearchKey({ key: "Escape", isComposing: true }, false),
    false,
  )
  assert.equal(
    canCloseSearchKey({ key: "Escape", defaultPrevented: true }, false),
    false,
  )
  assert.equal(
    sameQuery(
      { keywords: "x", scope: 0, parent: "/" },
      { keywords: "x", scope: 0, parent: "/" },
    ),
    true,
  )
  assert.equal(
    sameQuery(
      { keywords: "x", scope: 0, parent: "/" },
      { keywords: "x", scope: 0, parent: "/a" },
    ),
    false,
  )
})
