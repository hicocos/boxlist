import assert from "node:assert/strict"
import { readFile, readdir } from "node:fs/promises"
import test from "node:test"
import {
  ICON_TARGETS,
  ICON_ASSET_IDS,
  DEFAULT_ICON,
  canonicalIconConfig,
  defaultIconConfig,
  recommendedIconConfig,
  normalizeIconConfig,
  strictIconConfig,
  iconTargetId,
  resolveIconAsset,
  readIconConfig,
  writeIconConfig,
  editableManagedHead,
  mergeIconManagedHead,
  persistIconConfig,
} from "../src/utils/icon-policy.ts"

test("other-file overrides cannot steal unknown categories", () => {
  const config = recommendedIconConfig()
  config.selections.other = "pack-document"
  assert.equal(resolveIconAsset(config, 3, "music.unknown-audio"), "pack-music")
  assert.equal(resolveIconAsset(config, 2, "video.unknown-video"), "pack-video")
  assert.equal(resolveIconAsset(config, 5, "image.unknown-image"), "pack-image")
  assert.equal(resolveIconAsset(config, 0, "archive.zip.001", true), undefined)
  assert.equal(resolveIconAsset(config, 0, "unknown.bin"), "pack-document")
})

const glass =
  '<!-- OPENLIST-HOME-GLASS-START -->\n<style id="keep-glass">:root{--keep:1}</style>\n<!-- OPENLIST-HOME-GLASS-END -->'
const custom =
  '<meta name="test-user" content="preserved">\n<script>window.USER_DATA = "unchanged"</script>'
const legacy =
  '<!-- OPENLIST-FOLDER-ICON-START -->\n<script>window.OPENLIST_FOLDER_ICON_STYLE = "smile";</script>\n<!-- OPENLIST-FOLDER-ICON-END -->'

test("every target and asset is enumerated once and every recommended asset exists", () => {
  assert.equal(new Set(ICON_TARGETS.map((t) => t.id)).size, ICON_TARGETS.length)
  assert.equal(ICON_ASSET_IDS.size, 21)
  for (const target of ICON_TARGETS)
    if (target.recommended)
      assert.ok(ICON_ASSET_IDS.has(target.recommended), target.id)
})
test("all imported pack assets and legacy smile are retained on disk", async () => {
  const files = await readdir(
    new URL("../src/assets/file-icons/", import.meta.url),
  )
  assert.deepEqual(
    files.filter((x) => x.endsWith(".webp")).sort(),
    [...ICON_ASSET_IDS].map((x) => x + ".webp").sort(),
  )
  const assets = await readFile(
    new URL("../src/utils/icon-assets.ts", import.meta.url),
    "utf8",
  )
  for (const id of ICON_ASSET_IDS) assert.ok(assets.includes(`id: "${id}"`), id)
})
test("unconfigured formats remain default, invalid versions and URLs are discarded", () => {
  const empty = defaultIconConfig()
  for (const target of ICON_TARGETS)
    assert.equal(
      resolveIconAsset(
        empty,
        target.type,
        target.example,
        target.group === "archive",
      ),
      undefined,
      target.id,
    )
  for (const value of [
    null,
    [],
    { version: 2, selections: { folder: "pack-folder" } },
    { version: 1, selections: [] },
  ])
    assert.deepEqual(normalizeIconConfig(value), empty)
  assert.deepEqual(
    normalizeIconConfig({
      version: 1,
      selections: {
        folder: "https://bad.test/icon.png",
        other: "javascript:alert(1)",
        __proto__: "pack-doc",
        fake: "pack-doc",
      },
    }),
    empty,
  )
  assert.throws(() =>
    strictIconConfig({ version: 1, selections: { folder: "bad-id" } }),
  )
})
test("matching preset handles types and exact uppercase suffixes while preserving missing formats", () => {
  const config = recommendedIconConfig()
  assert.equal(resolveIconAsset(config, 1, "report.mp3"), "pack-folder")
  assert.equal(resolveIconAsset(config, 3, "TRACK.MP3"), "pack-mp3")
  assert.equal(resolveIconAsset(config, 3, "track.flac"), "pack-music")
  assert.equal(resolveIconAsset(config, 3, "track.wav"), "pack-wav")
  assert.equal(resolveIconAsset(config, 2, "movie.mkv"), "pack-video")
  assert.equal(resolveIconAsset(config, 0, "movie.m3u8"), "pack-video")
  assert.equal(resolveIconAsset(config, 5, "photo.png"), "pack-image")
  assert.equal(resolveIconAsset(config, 4, "notes.txt"), "pack-txt")
  assert.equal(resolveIconAsset(config, 4, "script.py"), "pack-document")
  assert.equal(resolveIconAsset(config, 0, "report.xlsx"), "pack-xlsx")
  assert.equal(resolveIconAsset(config, 0, "report.xls"), undefined)
  assert.equal(resolveIconAsset(config, 4, "README.md"), undefined)
  assert.equal(resolveIconAsset(config, 0, "design.psd"), "pack-psd")
  assert.equal(resolveIconAsset(config, 0, "design.ai"), undefined)
  assert.equal(resolveIconAsset(config, 0, "archive.cab"), "pack-cab")
  assert.equal(resolveIconAsset(config, 0, "archive.7z", true), undefined)
  assert.equal(resolveIconAsset(config, 0, "disk.iso", true), undefined)
  assert.equal(iconTargetId(0, "mp3"), "other")
})
test("specific suffix overrides category and explicit native default prevents inheritance", () => {
  const config = recommendedIconConfig()
  config.selections["ext:flac"] = "pack-wav"
  assert.equal(resolveIconAsset(config, 3, "track.flac"), "pack-wav")
  config.selections["ext:flac"] = DEFAULT_ICON
  assert.equal(resolveIconAsset(config, 3, "track.flac"), undefined)
  delete config.selections["ext:flac"]
  assert.equal(resolveIconAsset(config, 3, "track.flac"), "pack-music")
  config.selections.archive = "pack-rar"
  assert.equal(resolveIconAsset(config, 0, "archive.7z", true), "pack-rar")
  assert.equal(resolveIconAsset(config, 0, "archive.zip"), "pack-zip")
})
test("saved head round-trips without executable configuration and without touching custom or glass", () => {
  const config = recommendedIconConfig()
  const head = writeIconConfig(custom + "\n" + legacy + "\n" + glass, config)
  assert.ok(head.includes(custom))
  assert.ok(head.includes(glass))
  assert.equal(head.includes("OPENLIST-FOLDER-ICON-START"), false)
  assert.equal((head.match(/OPENLIST-ICON-THEME-START/g) || []).length, 1)
  assert.equal(
    canonicalIconConfig(readIconConfig(head)),
    canonicalIconConfig(config),
  )
  assert.equal(writeIconConfig(head, config), head)
  assert.ok(head.includes('<meta id="openlist-icon-config"'))
  assert.equal(
    head.slice(head.indexOf("OPENLIST-ICON-THEME-START")).includes("<script"),
    false,
  )
})
test("legacy folder style migrates and corrupt input falls back safely", () => {
  assert.equal(readIconConfig(legacy).selections.folder, "legacy-smile")
  assert.deepEqual(readIconConfig(""), defaultIconConfig())
  assert.deepEqual(
    readIconConfig(
      '<!-- OPENLIST-ICON-THEME-START --><meta id="openlist-icon-config" content="%broken"><!-- OPENLIST-ICON-THEME-END -->',
    ),
    defaultIconConfig(),
  )
})
test("raw head editing hides owned blocks and retains latest icon choices", () => {
  const initial = writeIconConfig(custom + "\n" + glass, defaultIconConfig())
  const latest = writeIconConfig(custom + "\n" + glass, recommendedIconConfig())
  assert.equal(editableManagedHead(initial), custom)
  const merged = mergeIconManagedHead(
    custom + '\n<meta name="new-edit">',
    latest,
  )
  assert.ok(merged.includes('<meta name="new-edit">'))
  assert.ok(merged.includes(glass))
  assert.equal(
    canonicalIconConfig(readIconConfig(merged)),
    canonicalIconConfig(recommendedIconConfig()),
  )
})
test("save reads latest head, writes only intended setting, then checks exact read-back", async () => {
  let head = {
    key: "customize_head",
    value: custom + "\n" + glass,
    flag: 0,
    group: 2,
  }
  let reads = 0,
    writes = 0
  const result = await persistIconConfig(
    {
      read: async () => {
        reads++
        return { ...head }
      },
      save: async (item) => {
        writes++
        assert.equal(item.key, "customize_head")
        assert.equal(item.flag, 0)
        head = item
      },
    },
    recommendedIconConfig(),
    defaultIconConfig(),
  )
  assert.equal(reads, 2)
  assert.equal(writes, 1)
  assert.equal(
    canonicalIconConfig(result),
    canonicalIconConfig(recommendedIconConfig()),
  )
  assert.ok(head.value.includes(custom))
  assert.ok(head.value.includes(glass))
})
test("failed, mismatched and stale writes never claim save success", async () => {
  const transport = {
    read: async () => ({ value: custom }),
    save: async () => {},
  }
  await assert.rejects(
    persistIconConfig(transport, recommendedIconConfig(), defaultIconConfig()),
    /未通过核对/,
  )
  await assert.rejects(
    persistIconConfig(
      {
        ...transport,
        save: async () => {
          throw new Error("offline")
        },
      },
      recommendedIconConfig(),
      defaultIconConfig(),
    ),
    /offline/,
  )
  let writes = 0
  const latest = writeIconConfig(custom, recommendedIconConfig())
  await assert.rejects(
    persistIconConfig(
      {
        read: async () => ({ value: latest }),
        save: async () => {
          writes++
        },
      },
      defaultIconConfig(),
      defaultIconConfig(),
    ),
    /另一页面/,
  )
  assert.equal(writes, 0)
})
test("admin route is independent and legacy style setting is absent from rendered settings", async () => {
  const menu = await readFile(
    new URL("../src/pages/manage/sidemenu_items.tsx", import.meta.url),
    "utf8",
  )
  assert.match(menu, /to: "\/@manage\/icons"/)
  assert.match(menu, /import\("\.\/icons\/IconManager"\)/)
  const common = await readFile(
    new URL("../src/pages/manage/settings/Common.tsx", import.meta.url),
    "utf8",
  )
  assert.equal(common.includes("loadFolderIconSetting"), false)
  assert.equal(common.includes("folderIconSetting()"), false)
  const panel = await readFile(
    new URL("../src/pages/manage/icons/IconManager.tsx", import.meta.url),
    "utf8",
  )
  assert.match(panel, /UserMethods\.is_admin\(me\(\)\)/)
  assert.match(panel, /type="radio"/)
})
