import test from "node:test"
import assert from "node:assert/strict"
import {
  ICON_TARGETS,
  defaultIconConfig,
  recommendedIconConfig,
  filterIconManagerTargets,
  iconSelectionValue,
  selectIconForTarget,
  changedIconTargetIds,
  resolveIconAsset,
} from "../src/utils/icon-policy.ts"
import { readFile } from "node:fs/promises"
const target = (id) => ICON_TARGETS.find((item) => item.id === id)
test("common view includes every general category and every supplied format asset", () => {
  const rows = filterIconManagerTargets("common", "")
  assert.equal(new Set(rows.map((t) => t.id)).size, rows.length)
  assert.ok(rows.length < ICON_TARGETS.length)
  for (const t of ICON_TARGETS.filter(
    (t) => !t.id.startsWith("ext:") || t.recommended,
  ))
    assert.ok(
      rows.some((r) => r.id === t.id),
      t.id,
    )
  assert.equal(filterIconManagerTargets("all", "").length, ICON_TARGETS.length)
})
test("nonblank searches cross category boundaries and accept uppercase/dotted suffixes", () => {
  for (const group of [
    "common",
    "general",
    "audio",
    "image",
    "office",
    "all",
  ]) {
    assert.deepEqual(
      filterIconManagerTargets(group, " .FLAC ").map((t) => t.id),
      ["ext:flac"],
    )
    assert.deepEqual(
      filterIconManagerTargets(group, "XLSX").map((t) => t.id),
      ["ext:xlsx"],
    )
  }
  assert.ok(
    filterIconManagerTargets("general", "音频").some((t) => t.id === "ext:mp3"),
  )
  assert.equal(filterIconManagerTargets("all", "this-does-not-exist").length, 0)
})
test("pending-change filter uses selection changes rather than the number of saved custom assets", () => {
  const baseline = recommendedIconConfig()
  assert.deepEqual(changedIconTargetIds(baseline, baseline), [])
  let draft = selectIconForTarget(baseline, target("ext:mp3"), "pack-wav")
  draft = selectIconForTarget(draft, target("folder"), "legacy-smile")
  const ids = changedIconTargetIds(draft, baseline)
  assert.deepEqual(new Set(ids), new Set(["folder", "ext:mp3"]))
  assert.deepEqual(
    filterIconManagerTargets("all", "", ids).map((t) => t.id),
    ["folder", "ext:mp3"],
  )
  assert.deepEqual(filterIconManagerTargets("image", "", ids), [])
  assert.deepEqual(
    changedIconTargetIds(
      selectIconForTarget(draft, target("folder"), "pack-folder"),
      baseline,
    ),
    ["ext:mp3"],
  )
})
test("picker selection preserves all other choices and retains native/inherit distinctions", () => {
  const original = recommendedIconConfig(),
    before = JSON.stringify(original)
  const native = selectIconForTarget(original, target("ext:mp3"), "default")
  assert.equal(iconSelectionValue(native, target("ext:mp3")), "default")
  assert.equal(resolveIconAsset(native, 3, "music.mp3"), undefined)
  const inherit = selectIconForTarget(native, target("ext:mp3"), "inherit")
  assert.equal(iconSelectionValue(inherit, target("ext:mp3")), "inherit")
  assert.equal(resolveIconAsset(inherit, 3, "music.mp3"), "pack-music")
  assert.equal(JSON.stringify(original), before)
  assert.equal(native.selections.folder, "pack-folder")
  assert.deepEqual(
    selectIconForTarget(defaultIconConfig(), target("folder"), "default"),
    defaultIconConfig(),
  )
  assert.throws(() =>
    selectIconForTarget(original, target("folder"), "inherit"),
  )
  assert.throws(() =>
    selectIconForTarget(original, target("folder"), "https://bad/icon"),
  )
})
test("explicit default cleanup is counted and inherited dependents do not inflate direct edits", () => {
  const baseline = {
    version: 1,
    selections: { folder: "default", "ext:pdf": "default" },
  }
  let draft = selectIconForTarget(baseline, target("folder"), "default")
  assert.deepEqual(changedIconTargetIds(draft, baseline), ["folder"])
  draft = selectIconForTarget(draft, target("ext:pdf"), "default")
  assert.deepEqual(changedIconTargetIds(draft, baseline), ["folder", "ext:pdf"])
  const inherited = selectIconForTarget(
    defaultIconConfig(),
    target("audio"),
    "pack-music",
  )
  assert.deepEqual(changedIconTargetIds(inherited, defaultIconConfig()), [
    "audio",
  ])
  assert.equal(resolveIconAsset(inherited, 3, "sample.flac"), "pack-music")
})
test("optimized panel has one save action, dialog focus ownership and no automatic page jump", async () => {
  const panel = await readFile(
    new URL("../src/pages/manage/icons/IconManager.tsx", import.meta.url),
    "utf8",
  )
  assert.equal((panel.match(/data-action="save"/g) || []).length, 1)
  assert.match(panel, /<dialog/)
  assert.match(panel, /editor\.showModal\(\)/)
  assert.match(panel, /onCancel=/)
  assert.match(panel, /data-action="done"/)
  assert.match(panel, /aria-haspopup="dialog"/)
  assert.doesNotMatch(panel, /scrollIntoView/)
  assert.match(panel, /focus\(\{ preventScroll: true \}\)/)
  const css = await readFile(
    new URL("../src/pages/manage/icons/icons.css", import.meta.url),
    "utf8",
  )
  assert.match(css, /position: sticky/)
  assert.match(css, /\.im-picker-body[\s\S]*?overflow-y: auto/)
})
