import test from "node:test"
import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import {
  isUploadedIconId,
  normalizeIconConfig,
  strictIconConfig,
  selectIconForTarget,
  defaultIconConfig,
  ICON_TARGETS,
  resolveIconAsset,
  readIconConfig,
  writeIconConfig,
  reconcileIconDraft,
  parseUploadedIconAsset,
  parseUploadedIconCatalog,
  parseIconLibraryCatalog,
  iconUploadFileError,
  assertIconAssetsAvailable,
  persistIconConfig,
} from "../src/utils/icon-policy.ts"
const id = "upload-" + "ab".repeat(16)
const asset = {
  id,
  name: "我的文件夹.png",
  width: 256,
  height: 256,
  bytes: 1000,
  created_at: "2026-10-06T12:00:00Z",
}
test("uploaded IDs accept only immutable server IDs, never URLs or traversal", () => {
  assert.equal(isUploadedIconId(id), true)
  for (const bad of [
    null,
    1,
    "upload-" + "A".repeat(32),
    "upload-" + "f".repeat(31),
    "../" + id,
    id + "/test.webp",
    id + ".png",
    "https://evil.test/x",
    id + "\n",
  ])
    assert.equal(isUploadedIconId(bad), false, String(bad))
  assert.deepEqual(
    normalizeIconConfig({ version: 1, selections: { folder: id } }).selections,
    { folder: id },
  )
  assert.throws(() =>
    strictIconConfig({ version: 1, selections: { folder: id + "/../bad" } }),
  )
})
test("uploaded icons use existing selection, head round trip and three-way merge", () => {
  const target = ICON_TARGETS.find((t) => t.id === "folder")
  const draft = selectIconForTarget(defaultIconConfig(), target, id)
  assert.equal(resolveIconAsset(draft, 1, "folder"), id)
  assert.deepEqual(
    readIconConfig(writeIconConfig('<meta name="keep">', draft)),
    draft,
  )
  const saved = { version: 1, selections: { audio: "pack-music" } }
  assert.deepEqual(
    reconcileIconDraft(draft, defaultIconConfig(), saved).draft.selections,
    { folder: id, audio: "pack-music" },
  )
})
test("catalog sanitizes fields and rejects untrusted URLs, invalid dimensions, duplicate IDs and excessive entries", () => {
  assert.deepEqual(
    parseUploadedIconAsset({
      ...asset,
      url: "https://evil.test/x",
      owner: "private",
    }),
    asset,
  )
  assert.deepEqual(parseUploadedIconCatalog([asset]), [asset])
  for (const patch of [
    { id: "../../escape" },
    { name: "\u0000bad" },
    { width: 512 },
    { bytes: 0 },
    { bytes: 5 * 1024 * 1024 + 1 },
    { created_at: 1 },
  ])
    assert.throws(() => parseUploadedIconAsset({ ...asset, ...patch }))
  assert.throws(() => parseUploadedIconCatalog([asset, asset]))
  assert.throws(() =>
    parseUploadedIconCatalog(Array.from({ length: 201 }, () => asset)),
  )
})
test("library hidden IDs allow only unique builtin names; removal checks and readbacks stay explicit", async () => {
  assert.deepEqual(
    parseIconLibraryCatalog({
      version: 1,
      assets: [asset],
      hidden_builtins: ["legacy-smile"],
    }),
    { assets: [asset], hidden_builtins: ["legacy-smile"] },
  )
  for (const hidden_builtins of [
    ["../bad"],
    [id],
    ["pack-folder", "pack-folder"],
    null,
  ])
    assert.throws(() =>
      parseIconLibraryCatalog({ version: 1, assets: [], hidden_builtins }),
    )
  const panel = await readFile(
    new URL("../src/pages/manage/icons/IconManager.tsx", import.meta.url),
    "utf8",
  )
  assert.match(panel, /assetUsage/)
  assert.match(panel, /deleteAsset/)
  assert.match(panel, /window\.confirm/)
  const request = await readFile(
    new URL("../src/pages/manage/icons/icon-upload.ts", import.meta.url),
    "utf8",
  )
  assert.match(request, /client\.delete/)
  assert.match(request, /catalog\.hidden_builtins\.includes/)
  assert.match(request, /confirmed/)
})
test("client file constraints force only PNG with finite bounded size", () => {
  for (const name of ["folder.png", "我的图标.PNG"])
    assert.equal(iconUploadFileError({ name, size: 200 }), "")
  for (const name of [
    "folder.jpg",
    "folder.webp",
    "folder.ico",
    "folder.svg",
    "folder.png.exe",
    "folder.png\n",
  ])
    assert.ok(iconUploadFileError({ name, size: 200 }))
  for (const size of [0, -1, 5 * 1024 * 1024 + 1, NaN, Infinity])
    assert.ok(iconUploadFileError({ name: "ok.png", size }))
})
test("uploader uses png-only picker and same task IDs for retry without replacing site settings", async () => {
  const uploader = await readFile(
    new URL("../src/pages/manage/icons/IconUploader.tsx", import.meta.url),
    "utf8",
  )
  assert.match(uploader, /accept="\.png,image\/png"/)
  assert.match(uploader, /requestId: job\.id/)
  assert.match(uploader, /uploadController\?\.abort\(/)
  assert.match(uploader, /previewControl/)
  assert.doesNotMatch(uploader, /saveIconSettings|customize_head|localStorage/)
  const request = await readFile(
    new URL("../src/pages/manage/icons/icon-upload.ts", import.meta.url),
    "utf8",
  )
  assert.match(request, /Authorization: authorization/)
  assert.match(request, /"X-Upload-Id": request\.requestId/)
  assert.match(request, /parseUploadedIconAsset/)
})
test("fresh catalogs reject removed uploads and hidden builtin references without rejecting defaults", () => {
  const config = (selections) => ({ version: 1, selections })
  const empty = { assets: [], hidden_builtins: [] }
  assert.doesNotThrow(() => assertIconAssetsAvailable(config({}), empty))
  assert.doesNotThrow(() =>
    assertIconAssetsAvailable(
      config({ folder: "default", audio: "pack-music" }),
      empty,
    ),
  )
  assert.doesNotThrow(() =>
    assertIconAssetsAvailable(config({ folder: id }), {
      ...empty,
      assets: [asset],
    }),
  )
  assert.throws(
    () => assertIconAssetsAvailable(config({ folder: id }), empty),
    { name: "IconAssetUnavailable" },
  )
  assert.throws(
    () =>
      assertIconAssetsAvailable(config({ folder: "pack-folder" }), {
        ...empty,
        hidden_builtins: ["pack-folder"],
      }),
    { name: "IconAssetUnavailable" },
  )
})
test("asset validation runs before writes and before idempotent confirmation; failures remain editable", async () => {
  const draft = { version: 1, selections: { folder: id } }
  let current = defaultIconConfig()
  let writes = 0
  let catalog = { assets: [], hidden_builtins: [] }
  const transport = {
    read: async () => ({ value: writeIconConfig("<meta name=keep>", current) }),
    save: async (item) => {
      writes++
      current = readIconConfig(item.value)
    },
    validate: async (config) => assertIconAssetsAvailable(config, catalog),
  }
  await assert.rejects(persistIconConfig(transport, draft, current), {
    name: "IconAssetUnavailable",
  })
  assert.equal(writes, 0)
  assert.deepEqual(current, defaultIconConfig())
  catalog = { assets: [asset], hidden_builtins: [] }
  assert.deepEqual(await persistIconConfig(transport, draft, current), draft)
  assert.equal(writes, 1)
  catalog = { assets: [], hidden_builtins: [] }
  await assert.rejects(persistIconConfig(transport, draft, draft), {
    name: "IconAssetUnavailable",
  })
  assert.equal(writes, 1)
  transport.validate = async () => {
    throw new Error("Synthetic catalog offline")
  }
  await assert.rejects(
    persistIconConfig(transport, draft, draft),
    /Synthetic catalog offline/,
  )
  assert.equal(writes, 1)
})
test("stopped tasks protect navigation while read-only refresh preserves retry IDs, and library restores its own opener", async () => {
  const uploader = await readFile(
    new URL("../src/pages/manage/icons/IconUploader.tsx", import.meta.url),
    "utf8",
  )
  const panel = await readFile(
    new URL("../src/pages/manage/icons/IconManager.tsx", import.meta.url),
    "utf8",
  )
  const settings = await readFile(
    new URL("../src/pages/manage/icons/icon-settings.ts", import.meta.url),
    "utf8",
  )
  const load = uploader.slice(
    uploader.indexOf("const loadCatalog"),
    uploader.indexOf("onMount", uploader.indexOf("const loadCatalog")),
  )
  assert.doesNotMatch(load, /pending\(\)/)
  assert.match(uploader, /props\.unfinished\?\.\(jobs\(\)\.some/)
  assert.match(uploader, /data-action="clear-upload"/)
  assert.match(panel, /uploadBusy\(\) \|\| uploadPending\(\)/)
  assert.match(panel, /returningToLibrary/)
  assert.match(panel, /openedFrom = from/)
  assert.match(settings, /validate: async/)
  assert.match(settings, /assertIconAssetsAvailable\(config, catalog\)/)
})
