import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import ts from "typescript"

function load(path, imports, globals = {}) {
  const text = readFileSync(new URL(path, import.meta.url), "utf8")
  const output = ts.transpileModule(text, {
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
const pure = load("../src/store/navigation-state.ts", {})

function harness() {
  const microtasks = [],
    timers = [],
    frames = [],
    handlers = {}
  const location = { href: "https://test.invalid/" }
  class Element {
    constructor(name, index, top) {
      this.dataset = { name, index: String(index) }
      this.top = top
      this.classes = new Set()
      this.classList = {
        add: (c) => this.classes.add(c),
        remove: (c) => this.classes.delete(c),
      }
    }
    getBoundingClientRect() {
      return { top: this.top, bottom: this.top + 48 }
    }
    closest(selector) {
      if (selector === "[data-name]") return this
      if (selector === "a[href]") return this
      return null
    }
    hasAttribute() {
      return false
    }
    getAttribute() {
      return pure.directoryHref("/" + this.dataset.name)
    }
    target = ""
  }
  const rows = [new Element("first", 0, 200), new Element("opened", 1, 400)]
  const store = {
    state: 4,
    objs: [{ name: "first" }, { name: "opened" }],
    err: "",
    total: 2,
  }
  const window = {
    scrollY: 0,
    innerHeight: 844,
    addEventListener() {},
    scrollTo({ top }) {
      this.scrollY = top
    },
    scrollBy({ top }) {
      this.scrollY += top
    },
  }
  const document = {
    querySelectorAll: () => rows,
    addEventListener: (type, fn) => (handlers[type] = fn),
  }
  const signal = (initial) => {
    let value = initial
    return [
      () => value,
      (next) => (value = typeof next === "function" ? next(value) : next),
    ]
  }
  const history = load(
    "../src/store/history.ts",
    {
      "solid-js": {
        createSignal: signal,
        createRoot: (fn) => fn(),
        createComputed: (fn) => fn(),
      },
      "~/store/obj": {
        shouldKeepState: () => false,
        ObjStore: {
          setState: (v) => (store.state = v),
          set: (v) => Object.assign(store, v),
        },
        objStore: store,
        State: { Folder: 4, FetchingMore: 3, Initial: 0 },
        password: () => "",
      },
      "./user": {
        me: () => ({
          id: 1,
          username: "guest",
          role: 1,
          permission: 0,
          base_path: "/",
        }),
      },
      "./settings": {
        getPagination: () => ({ type: "auto_load_more", size: 100 }),
      },
      "~/utils": {
        base_path: "",
        r: { defaults: { headers: { common: {} } } },
      },
      "./navigation-state": pure,
      "./directory-navigation": {
        cancelDirectoryRequests() {},
        getDirectoryPage: () => 1,
        setDirectoryPage() {},
      },
    },
    {
      window,
      document,
      location,
      Element,
      queueMicrotask: (fn) => microtasks.push(fn),
      setTimeout: (fn, delay) => timers.push({ fn, delay }),
      requestAnimationFrame: (fn) => frames.push(fn),
    },
  )
  const flushMicrotasks = () => {
    while (microtasks.length) microtasks.shift()()
  }
  const flushFrames = () => {
    while (frames.length) frames.shift()()
  }
  flushMicrotasks()
  history.setHistoryLocation("/")
  return {
    history,
    rows,
    store,
    location,
    handlers,
    timers,
    flushMicrotasks,
    flushFrames,
  }
}

test("router microtask cannot overwrite the captured clicked folder with first visible row", async () => {
  const h = harness()
  h.handlers.click({ button: 0, target: h.rows[1] })
  assert.equal(h.history.HistoryMap.get("/").openedName, "opened")
  // Capture listeners run first, then Solid starts its router transition in a microtask.
  h.flushMicrotasks()
  h.location.href = "https://test.invalid/opened"
  h.history.recordCurrentDirectory()
  assert.equal(h.history.HistoryMap.get("/").anchor.name, "opened")
  h.timers.filter((x) => x.delay === 0).forEach((x) => x.fn())
  const recovered = h.history.recoverHistory("/")
  h.flushFrames()
  assert.equal(await recovered, true)
  assert.equal(h.rows[1].classes.has("directory-return-highlight"), true)
  assert.equal(h.rows[0].classes.has("directory-return-highlight"), false)
})

test("viewport-only snapshots restore scroll without highlighting an unrelated first row", async () => {
  const h = harness()
  h.history.recordCurrentDirectory()
  assert.equal(h.history.HistoryMap.get("/").anchor.name, "first")
  assert.equal(h.history.HistoryMap.get("/").openedName, undefined)
  const recovered = h.history.recoverHistory("/")
  h.flushFrames()
  await recovered
  assert.ok(
    h.rows.every((row) => !row.classes.has("directory-return-highlight")),
  )
})

test("a prevented non-navigation click unlocks capture for the next real target", () => {
  const h = harness()
  h.handlers.click({ button: 0, target: h.rows[0] })
  h.timers.filter((x) => x.delay === 0).forEach((x) => x.fn())
  h.handlers.click({ button: 0, target: h.rows[1] })
  assert.equal(h.history.HistoryMap.get("/").openedName, "opened")
})

test("stable DOM name wins over stale index while sorted rows settle", async () => {
  const h = harness()
  h.rows[1].dataset.index = "0"
  h.handlers.click({ button: 0, target: h.rows[1] })
  assert.equal(h.history.HistoryMap.get("/").openedName, "opened")
  h.rows.reverse()
  const recovered = h.history.recoverHistory("/")
  h.flushFrames()
  await recovered
  assert.equal(
    h.rows.find((row) => row.classes.has("directory-return-highlight")).dataset
      .name,
    "opened",
  )
})
