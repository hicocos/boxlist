import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"
import ts from "typescript"

// Execute production TypeScript in Node without changing project configuration.
async function loadTS(relative, transform = (source) => source) {
  const source = transform(
    await readFile(new URL(relative, import.meta.url), "utf8"),
  )
  const { outputText } = ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ES2022,
    },
  })
  return import(
    `data:text/javascript;base64,${Buffer.from(outputText).toString("base64")}`
  )
}
const { requestImage, createImageRequests, neighbourIndices } = await loadTS(
  "../src/pages/home/previews/image-requests.ts",
)
const { readerCommand, isPlainReaderClick } = await loadTS(
  "../src/pages/reader/reader-command.ts",
)
const { wantsAutoFullscreen, setNativeFullscreen } = await loadTS(
  "../src/pages/home/previews/video-fullscreen.ts",
)
const { encodePath, pathDir, pathJoin, joinBase } = await loadTS(
  "../src/utils/path.ts",
  (s) =>
    s.replace('import { base_path } from "."', 'const base_path = "/mount"'),
)

class FakeImage {
  src = ""
  naturalWidth = 800
  naturalHeight = 1200
  onload = null
  onerror = null
  removeAttribute(name) {
    if (name === "src") this.src = ""
  }
}
const makePool = (timeout = 30_000) => {
  const images = []
  const requests = createImageRequests(() => {
    const image = new FakeImage()
    images.push(image)
    return image
  }, timeout)
  return { images, requests }
}

test("image request timeout clears handlers and source; late load cannot revive it", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] })
  const image = new FakeImage()
  const request = requestImage(image, "late.jpg", 30_000)
  const late = image.onload
  const rejected = assert.rejects(request.promise, { reason: "timeout" })
  t.mock.timers.tick(30_000)
  late()
  await rejected
  assert.equal(image.onload, null)
  assert.equal(image.onerror, null)
  assert.equal(image.src, "")
  request.cancel()
})

test("pending timeout releases deduplication lease and permits same-URL retry", async (t) => {
  t.mock.timers.enable({ apis: ["setTimeout"] })
  const { images, requests } = makePool()
  const first = requests.load("same.jpg")
  assert.equal(requests.load("same.jpg"), first)
  const rejected = assert.rejects(first, { reason: "timeout" })
  t.mock.timers.tick(30_000)
  await rejected
  assert.equal(requests.pending.size, 0)
  const retry = requests.load("same.jpg")
  assert.equal(images.length, 2)
  images[1].onload()
  assert.deepEqual(await retry, { src: "same.jpg", width: 800, height: 1200 })
  assert.equal(requests.pending.size, 0)
  requests.dispose()
})

test("cancellation and late rejection cannot erase a newer request generation", async () => {
  const { images, requests } = makePool()
  const first = requests.load("same.jpg")
  const late = images[0].onload
  const rejected = assert.rejects(first, { reason: "cancelled" })
  requests.forget("same.jpg")
  const second = requests.load("same.jpg")
  late()
  await rejected
  assert.equal(requests.pending.size, 1)
  assert.equal(requests.cache.size, 0)
  images[1].onload()
  await second
  assert.equal(requests.pending.size, 0)
  assert.equal(requests.cache.size, 1)
  requests.dispose()
})

test("invalid dimensions fail, valid cached load deduplicates without Image objects", async () => {
  const { images, requests } = makePool()
  const failed = requests.load("bad.svg")
  const rejected = assert.rejects(failed, { reason: "error" })
  images[0].naturalWidth = 0
  images[0].onload()
  await rejected
  const valid = requests.load("good.webp")
  images[1].onload()
  await valid
  assert.equal((await requests.load("good.webp")).height, 1200)
  assert.equal(images.length, 2)
  requests.dispose()
})

test("retain cancels off-window requests; unmount settles all promises and cache", async () => {
  const { images, requests } = makePool()
  const first = requests.load("old.jpg")
  const second = requests.load("current.jpg")
  const oldRejected = assert.rejects(first, { reason: "cancelled" })
  const currentRejected = assert.rejects(second, { reason: "cancelled" })
  requests.retain(["current.jpg"])
  await oldRejected
  assert.equal(requests.pending.size, 1)
  requests.dispose()
  await currentRejected
  assert.equal(requests.pending.size, 0)
  assert.equal(requests.cache.size, 0)
  for (const image of images) {
    assert.equal(image.onload, null)
    assert.equal(image.onerror, null)
    assert.equal(image.src, "")
  }
  await assert.rejects(requests.load("after-unmount.jpg"), {
    reason: "cancelled",
  })
})

test("neighbour preloading stays adjacent, size bounded and data-saving aware", () => {
  const list = [
    { size: 1 },
    { size: 2 },
    { size: 21 * 1024 * 1024 },
    { size: 3 },
  ]
  assert.deepEqual(neighbourIndices(1, list), [0])
  assert.deepEqual(neighbourIndices(2, list), [1, 3])
  assert.deepEqual(neighbourIndices(0, list), [1])
  assert.deepEqual(neighbourIndices(2, list, { saveData: true }), [])
  assert.deepEqual(neighbourIndices(2, list, { effectiveType: "2g" }), [])
  assert.deepEqual(neighbourIndices(2, list, { effectiveType: "slow-2g" }), [])
})

test("repeated reader jumps have distinct identities; fraction is bounded", () => {
  const first = readerCommand(5, 0)
  const second = readerCommand(5, 0)
  assert.deepEqual(first, second)
  assert.notEqual(first, second)
  assert.deepEqual(readerCommand(1.8, 2), { index: 1, fraction: 1 })
  assert.deepEqual(readerCommand(NaN, -1), { index: 0, fraction: 0 })
})

test("return navigation preserves modified clicks and non-primary buttons", () => {
  const event = {
    button: 0,
    defaultPrevented: false,
    altKey: false,
    ctrlKey: false,
    metaKey: false,
    shiftKey: false,
  }
  assert.equal(isPlainReaderClick(event), true)
  for (const modifier of [
    "altKey",
    "ctrlKey",
    "metaKey",
    "shiftKey",
    "defaultPrevented",
  ]) {
    assert.equal(isPlainReaderClick({ ...event, [modifier]: true }), false)
  }
  assert.equal(isPlainReaderClick({ ...event, button: 1 }), false)
})

test("actual path encoder preserves special video names and base path once", () => {
  for (const name of [
    "第二集 #1?100%.mp4",
    "literal%23%3F.mp4",
    "正常中文.mp4",
  ]) {
    const href =
      joinBase(
        encodePath(pathJoin(pathDir("/中文#?%/第一集.mp4"), name), true),
      ) + "?auto_fullscreen=true"
    const url = new URL(href, "https://example.test")
    assert.equal(decodeURIComponent(url.pathname), `/mount/中文#?%/${name}`)
    assert.equal(url.hash, "")
    assert.equal(url.searchParams.get("auto_fullscreen"), "true")
  }
})

test("auto fullscreen only accepts literal true; false cannot fall through", () => {
  assert.equal(wantsAutoFullscreen("true"), true)
  for (const value of ["false", undefined, "", "TRUE", true, ["true"]]) {
    assert.equal(wantsAutoFullscreen(value), false)
  }
})

test("native fullscreen handles async rejection and unsupported/no-op setters", async () => {
  const rejected = Object.defineProperty({}, "fullscreen", {
    get: () => false,
    set: async () => {
      throw new Error("User activation required")
    },
  })
  assert.equal(await setNativeFullscreen(rejected, true), false)
  assert.equal(await setNativeFullscreen({}, true), false)
  const noop = Object.defineProperty({}, "fullscreen", {
    get: () => false,
    set: () => {},
  })
  assert.equal(await setNativeFullscreen(noop, true), false)
  let state = false
  const allowed = Object.defineProperty({}, "fullscreen", {
    get: () => state,
    set: async (value) => {
      state = value
    },
  })
  assert.equal(await setNativeFullscreen(allowed, true), true)
  assert.equal(state, true)
  assert.equal(await setNativeFullscreen(allowed, false), true)
  assert.equal(state, false)
})

test("reader integration separates scroll reports and commands, both exits restore history", async () => {
  const reader = await readFile(
    new URL("../src/pages/reader/Reader.tsx", import.meta.url),
    "utf8",
  )
  const continuous = await readFile(
    new URL("../src/pages/reader/ContinuousReader.tsx", import.meta.url),
    "utf8",
  )
  const progress = reader.slice(
    reader.indexOf("  const progress ="),
    reader.indexOf("  const jump ="),
  )
  assert.doesNotMatch(progress, /setCommand/)
  assert.match(reader, /setCommand\(readerCommand\(next, part\)\)/)
  assert.match(reader, /preserveHistory: true, scroll: false/)
  assert.equal((reader.match(/onClick=\{returnToDirectory\}/g) || []).length, 3)
  assert.equal((reader.match(/data-restore-history/g) || []).length, 3)
  assert.match(continuous, /props\.items, props\.command/)
  assert.match(continuous, /image\.tabIndex = 0/)
  assert.match(continuous, /event\.key === "Enter" \|\| event\.key === " "/)
  assert.match(
    continuous,
    /"touchstart", interruptNavigation, \{\s*passive: true/,
  )
})
