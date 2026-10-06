import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import ts from "typescript"

function load(relative, imports = {}, globals = {}) {
  const source = readFileSync(new URL(relative, import.meta.url), "utf8")
  const output = ts.transpileModule(source, {
    compilerOptions: {
      module: ts.ModuleKind.CommonJS,
      target: ts.ScriptTarget.ES2022,
    },
  }).outputText
  const module = { exports: {} }
  new Function("module", "exports", "require", ...Object.keys(globals), output)(
    module,
    module.exports,
    (name) => {
      assert.ok(name in imports, `Unexpected import: ${name}`)
      return imports[name]
    },
    ...Object.values(globals),
  )
  return module.exports
}
const pure = load("../src/store/navigation-state.ts")
const signal = (initial) => {
  let value = initial
  return [
    () => value,
    (next) => (value = typeof next === "function" ? next(value) : next),
  ]
}

test("production capture snapshots before reader unmount, preserves return page and isolates accounts", () => {
  const documentHandlers = {}
  const windowHandlers = {}
  const microtasks = []
  const location = { href: "https://test.invalid/books?page=4" }
  const user = {
    id: 1,
    username: "one",
    role: 0,
    permission: 0,
    base_path: "/",
  }
  const store = { state: 4, objs: [{ name: "chapter" }], err: "", total: 200 }
  const imports = {
    "solid-js": {
      createSignal: signal,
      createRoot: (fn) => fn(),
      createComputed: (fn) => fn(),
    },
    "~/store/obj": {
      shouldKeepState: () => false,
      ObjStore: {
        setState: (value) => (store.state = value),
        set: (value) => Object.assign(store, value),
      },
      objStore: store,
      State: { Folder: 4, FetchingMore: 3 },
      password: () => "",
    },
    "./user": { me: () => user },
    "./settings": { getPagination: () => ({ type: "pagination", size: 30 }) },
    "~/utils": {
      base_path: "",
      r: { defaults: { headers: { common: { Authorization: "" } } } },
    },
    "./navigation-state": pure,
    "./directory-navigation": {
      cancelDirectoryRequests: () => {},
      getDirectoryPage: () => 4,
      setDirectoryPage: () => {},
    },
  }
  class Element {}
  const window = {
    scrollY: 470,
    innerHeight: 600,
    addEventListener: (type, handler) => (windowHandlers[type] = handler),
  }
  const document = {
    querySelectorAll: () => [],
    addEventListener: (type, handler, capture) => {
      assert.equal(capture, true)
      documentHandlers[type] = handler
    },
  }
  const h = load("../src/store/history.ts", imports, {
    window,
    document,
    location,
    Element,
    queueMicrotask: (fn) => microtasks.push(fn),
  })
  h.setHistoryLocation("/books", 4)
  const source = new Element()
  const link = {
    target: "",
    hasAttribute: () => false,
    getAttribute: () => "/@reader?dir=%2Fbooks",
  }
  source.closest = (selector) => (selector === "a[href]" ? link : null)
  documentHandlers.click({ button: 0, target: source })
  assert.equal(h.getDirectoryReturnHref("/books"), "/books?page=4")
  assert.equal(h.HistoryMap.get("/books?page=4").scroll, 470)
  // Reader routing resets scroll; cleanup must not overwrite the captured source.
  location.href = "https://test.invalid/@reader?dir=%2Fbooks"
  window.scrollY = 0
  microtasks.forEach((fn) => fn())
  h.recordCurrentDirectory()
  assert.equal(h.HistoryMap.get("/books?page=4").scroll, 470)
  h.releaseHistoryLocation()
  // Reader exit capture has no active directory and cannot corrupt it.
  link.getAttribute = () => "/books?page=4"
  link.hasAttribute = (name) => name === "data-restore-history"
  documentHandlers.click({ button: 0, target: source })
  assert.equal(h.hasHistory("/books", 4), true)
  // A normal navigation to the same target is fresh, not permanent old cache.
  link.hasAttribute = () => false
  documentHandlers.click({ button: 0, target: source })
  assert.equal(h.hasHistory("/books", 4), false)
  h.setHistoryLocation("/books", 4)
  h.recordCurrentDirectory()
  assert.equal(h.getDirectoryReturnHref("/books"), "/books?page=4")
  user.id = 2
  assert.equal(h.getDirectoryReturnHref("/books"), "/books")
  assert.equal(h.HistoryMap.size, 0)
})

test("router consumes preserveHistory itself and keeps normal navigation fresh", () => {
  const navigations = []
  const invalidations = []
  let captures = 0
  const { useRouter } = load("../src/hooks/useRouter.ts", {
    "@solidjs/router": {
      useNavigate:
        () =>
        (...args) =>
          navigations.push(args),
      useLocation: () => ({
        pathname: "/app/a",
        search: "",
        hash: "",
        query: {},
      }),
      useParams: () => ({}),
    },
    "solid-js": { createMemo: (fn) => fn, untrack: (fn) => fn() },
    "~/utils": {
      encodePath: pure.directoryHref,
      joinBase: (path) => `/app${path}`,
      log: () => {},
      trimBase: (path) => path.replace("/app", ""),
    },
    "~/store/history": {
      recordCurrentDirectory: () => captures++,
      invalidateHistoryHref: (path) => invalidations.push(path),
    },
  })
  const router = useRouter()
  router.to("/a?page=4", false, { preserveHistory: true, replace: true })
  assert.deepEqual(navigations[0], ["/app/a?page=4", { replace: true }])
  assert.deepEqual(invalidations, [])
  router.to("/a?page=4")
  assert.deepEqual(invalidations, ["/app/a?page=4"])
  assert.equal(captures, 2)
})
