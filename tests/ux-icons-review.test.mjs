import test from "node:test"
import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import {
  previewIconAsset,
  ICON_TARGETS,
  defaultIconConfig,
  recommendedIconConfig,
  persistIconConfig,
  canonicalIconConfig,
  writeIconConfig,
} from "../src/utils/icon-policy.ts"
import { withHeadWriteLock } from "../src/utils/head-write-lock.ts"

test("general previews cannot be overridden by an example suffix", () => {
  const config = recommendedIconConfig()
  for (const id of ["ext:mp4", "ext:flac", "ext:jpg", "ext:py", "ext:7z"])
    config.selections[id] = "default"
  for (const [id, asset] of [
    ["video", "pack-video"],
    ["audio", "pack-music"],
    ["image", "pack-image"],
    ["text", "pack-document"],
  ]) {
    assert.equal(
      previewIconAsset(
        config,
        ICON_TARGETS.find((t) => t.id === id),
      ),
      asset,
    )
  }
  config.selections.archive = "pack-zip"
  assert.equal(
    previewIconAsset(
      config,
      ICON_TARGETS.find((t) => t.id === "archive"),
    ),
    "pack-zip",
  )
  assert.equal(
    previewIconAsset(
      config,
      ICON_TARGETS.find((t) => t.id === "ext:mp4"),
    ),
    undefined,
  )
})

test("a committed save whose read-back failed is idempotently confirmed on retry", async () => {
  let stored = { value: "" },
    reads = 0,
    writes = 0
  const config = recommendedIconConfig()
  const transport = {
    read: async () => {
      reads++
      if (reads === 2) throw new Error("temporary network timeout")
      return { ...stored }
    },
    save: async (item) => {
      stored = item
      writes++
    },
  }
  await assert.rejects(
    persistIconConfig(transport, config, defaultIconConfig()),
    (error) => error.name === "IconSaveUncertain",
  )
  const saved = await persistIconConfig(transport, config, defaultIconConfig())
  assert.equal(writes, 1)
  assert.equal(canonicalIconConfig(saved), canonicalIconConfig(config))
})

test("head lock covers latest read, write and verification across overlapping saves", async () => {
  let queue = Promise.resolve(),
    stored = { value: '<meta content="OLD">' }
  const locks = {
    request: (_name, _options, operation) => {
      const job = queue.then(operation)
      queue = job.catch(() => {})
      return job
    },
  }
  let release, entered
  const paused = new Promise((resolve) => (release = resolve)),
    start = new Promise((resolve) => (entered = resolve))
  const edit = withHeadWriteLock(async () => {
    entered()
    await paused
    stored = { value: '<meta content="NEW">' }
  }, locks)
  await start
  let readCount = 0
  const icon = withHeadWriteLock(
    () =>
      persistIconConfig(
        {
          read: async () => {
            readCount++
            return { ...stored }
          },
          save: async (item) => {
            stored = item
          },
        },
        recommendedIconConfig(),
        defaultIconConfig(),
      ),
    locks,
  )
  assert.equal(readCount, 0)
  release()
  await Promise.all([edit, icon])
  assert.ok(stored.value.includes('content="NEW"'))
  assert.equal(stored.value.includes('content="OLD"'), false)
  assert.equal(readCount, 2)
})

test("unsupported locks fail before any write and all in-app head writers use the lock", async () => {
  let wrote = false
  await assert.rejects(
    withHeadWriteLock(async () => {
      wrote = true
    }, null),
    /不支持安全保存/,
  )
  assert.equal(wrote, false)
  for (const file of [
    "../src/pages/manage/icons/icon-settings.ts",
    "../src/pages/manage/settings/Common.tsx",
  ]) {
    const text = await readFile(new URL(file, import.meta.url), "utf8")
    assert.match(text, /withHeadWriteLock/)
  }
})

test("icons retain current custom choices and use route/history/focus recovery hooks", async () => {
  const panel = await readFile(
    new URL("../src/pages/manage/icons/IconManager.tsx", import.meta.url),
    "utf8",
  )
  assert.match(panel, /useBeforeLeave\(/)
  assert.match(panel, /registerIconLeaveGuard/)
  assert.match(panel, /editor\.focus\(/)
  assert.match(panel, /asset\.id === chosen/)
  assert.match(panel, /data-action="reconcile"/)
  const icon = await readFile(
    new URL("../src/components/SiteIcon.tsx", import.meta.url),
    "utf8",
  )
  assert.match(icon, /createEffect\(/)
  assert.match(icon, /"online", retry/)
  assert.match(icon, /ICON_CONFIG_EVENT, retry/)
})
