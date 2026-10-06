import test from "node:test"
import assert from "node:assert/strict"
import { readFileSync } from "node:fs"
import ts from "typescript"

// Transpile just this dependency-free production module: no app, API or storage.
const source = readFileSync(
  new URL("../src/store/navigation-state.ts", import.meta.url),
  "utf8",
)
const output = ts.transpileModule(source, {
  compilerOptions: {
    module: ts.ModuleKind.CommonJS,
    target: ts.ScriptTarget.ES2022,
  },
}).outputText
const module = { exports: {} }
new Function("module", "exports", output)(module, module.exports)
const {
  directoryHref,
  parseDirectoryHref,
  validPage,
  BoundedHistory,
  classifyDirectoryError,
  directoryErrorDetails,
  mergeDirectoryPage,
  collectDirectoryPages,
  NavigationEpoch,
} = module.exports

test("directory href encodes names once and preserves the original page", () => {
  assert.equal(
    directoryHref("/中文/a ?#%", 7),
    "/%E4%B8%AD%E6%96%87/a%20%3F%23%25?page=7",
  )
  assert.equal(directoryHref("", 1), "/")
  assert.equal(directoryHref("/a/", -2), "/a")
})
test("decode only pathname; isolate app base, origin and malformed escapes", () => {
  assert.deepEqual(
    parseDirectoryHref(
      "/drive/a%3F%23%25?page=4&pwd=secret",
      "https://host.test/",
      "/drive",
    ),
    { path: "/a?#%", page: 4 },
  )
  assert.equal(
    parseDirectoryHref(
      "https://else.test/drive/a",
      "https://host.test",
      "/drive",
    ),
    undefined,
  )
  assert.equal(
    parseDirectoryHref("/drive2/a", "https://host.test", "/drive"),
    undefined,
  )
  assert.equal(parseDirectoryHref("/%ZZ", "https://host.test"), undefined)
})
test("invalid pages are normalized, not partial parseInt values", () => {
  for (const value of [-1, 0, "2oops", Infinity, 1.5, null, NaN])
    assert.equal(validPage(value), 1)
  assert.equal(validPage("30"), 30)
})
test("history respects LRU count, byte budget, expiry and account clearing", () => {
  const cache = new BoundedHistory(2, 10, 100)
  cache.set("a", { page: 3 }, 4, 0)
  cache.set("b", { page: 6 }, 4, 0)
  assert.equal(cache.get("a", 1).page, 3)
  cache.set("c", {}, 4, 1)
  assert.equal(cache.get("b", 1), undefined)
  assert.equal(cache.size, 2)
  assert.equal(cache.set("large", {}, 11, 1), false)
  assert.equal(cache.get("a", 101), undefined)
  cache.clear()
  assert.equal(cache.size, 0)
})
test("permission beats missing detail and only password-specific 403 prompts", () => {
  assert.equal(
    classifyDirectoryError("private /x object not found", 403),
    "permission",
  )
  assert.equal(classifyDirectoryError("password is incorrect", 403), "password")
  assert.equal(classifyDirectoryError("object not found", 500), "missing")
  assert.equal(classifyDirectoryError("Network Error"), "network")
  assert.equal(classifyDirectoryError("timeout", 408), "network")
  assert.equal(classifyDirectoryError("storage not found", 500), "other")
  assert.equal(
    directoryErrorDetails("permission", 403),
    "permission (HTTP/API 403)",
  )
})
test("overlapping pages dedupe names without replacing existing records", () => {
  assert.deepEqual(
    mergeDirectoryPage(
      [{ name: "a", value: 1 }],
      [{ name: "a", value: 2 }, { name: "b" }, { name: "b" }],
    ),
    [{ name: "a", value: 1 }, { name: "b" }],
  )
})
test("refresh stops immediately on failed page (never repeats the same page)", async () => {
  const calls = []
  const result = await collectDirectoryPages({
    target: 50,
    size: 1,
    isCurrent: () => true,
    fetch: async (page) => {
      calls.push(page)
      return page === 2 ? undefined : { items: [page], total: 100 }
    },
  })
  assert.equal(result.ok, false)
  assert.deepEqual(calls, [1, 2])
  assert.deepEqual(result.items, [1])
})
test("refresh respects total shrink and does not request vanished pages", async () => {
  const calls = []
  const result = await collectDirectoryPages({
    target: 10,
    size: 2,
    isCurrent: () => true,
    fetch: async (page) => {
      calls.push(page)
      return { items: [page, page], total: page === 1 ? 20 : 4 }
    },
  })
  assert.equal(result.ok, true)
  assert.equal(result.page, 2)
  assert.deepEqual(calls, [1, 2])
})
test("empty page terminates a refresh despite stale server totals", async () => {
  let calls = 0
  const result = await collectDirectoryPages({
    target: 10,
    size: 2,
    isCurrent: () => true,
    fetch: async () => {
      calls++
      return { items: [], total: 1000 }
    },
  })
  assert.equal(result.ok, true)
  assert.equal(calls, 1)
})
test("navigation during an awaited page discards it and prevents follow-up", async () => {
  const epoch = new NavigationEpoch()
  const run = epoch.begin()
  let calls = 0
  const result = await collectDirectoryPages({
    target: 10,
    size: 1,
    isCurrent: () => epoch.matches(run),
    fetch: async () => {
      calls++
      epoch.begin()
      return { items: ["obsolete"], total: 10 }
    },
  })
  assert.equal(result.ok, false)
  assert.deepEqual(result.items, [])
  assert.equal(calls, 1)
})
