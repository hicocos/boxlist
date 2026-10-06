import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"
import {
  initialLocalSettings,
  localSettingResetEntries,
  normalizeLocalNumber,
} from "../src/utils/local-settings-policy.ts"

const numeric = initialLocalSettings.filter(
  (setting) => setting.type === "number",
)

for (const setting of numeric) {
  test(`${setting.key}: empty and malformed drafts preserve the saved value`, () => {
    for (const draft of [
      "",
      " ",
      "NaN",
      "Infinity",
      "-Infinity",
      "1e1000",
      "0x80",
      "abc",
      "12px",
      "1,5",
    ])
      assert.equal(
        normalizeLocalNumber(setting, draft, setting.default),
        setting.default,
        draft,
      )
  })
  test(`${setting.key}: clamped, integer-only and bounded`, () => {
    assert.equal(normalizeLocalNumber(setting, "-9999"), String(setting.min))
    assert.equal(
      normalizeLocalNumber(setting, "9999999999"),
      String(setting.max),
    )
    assert.equal(
      normalizeLocalNumber(setting, "14.6"),
      String(Math.max(setting.min, 15)),
    )
    assert.equal(
      normalizeLocalNumber(setting, ` ${setting.default} `),
      setting.default,
    )
  })
  test(`${setting.key}: repairs legacy invalid values without returning NaN`, () => {
    for (const previous of [
      null,
      undefined,
      "",
      "NaN",
      "Infinity",
      "garbage",
    ]) {
      assert.equal(normalizeLocalNumber(setting, "", previous), setting.default)
    }
    for (const absent of [null, undefined]) {
      assert.equal(
        normalizeLocalNumber(setting, absent, absent),
        setting.default,
      )
      assert.equal(
        normalizeLocalNumber(setting, setting.default, absent),
        setting.default,
      )
    }
    for (let number = -1000; number <= 3000; number += 17) {
      const output = Number(normalizeLocalNumber(setting, String(number / 3)))
      assert.ok(Number.isInteger(output))
      assert.ok(output >= setting.min && output <= setting.max)
    }
  })
}

test("settings are assigned once to explicit groups and preserve all existing keys", () => {
  const keys = initialLocalSettings.map((setting) => setting.key)
  assert.equal(new Set(keys).size, 14)
  assert.equal(keys.length, 14)
  assert.deepEqual(
    [...keys].sort(),
    [
      "aria2_rpc_url",
      "aria2_rpc_secret",
      "global_default_layout",
      "show_folder_in_image_view",
      "show_sidebar",
      "show_count_msg",
      "position_of_header_navbar",
      "grid_item_size",
      "list_item_filename_overflow",
      "open_item_on_checkbox",
      "editor_font_size",
      "editor_word_wrap",
      "editor_minimap",
      "show_gallery_thumbnails",
    ].sort(),
  )
  for (const setting of initialLocalSettings)
    assert.ok(
      ["appearance", "images", "editor", "downloads"].includes(setting.group),
    )
})

for (const group of ["appearance", "images", "editor", "downloads"]) {
  test(`${group}: reset changes only its safe keys, never credentials or progress`, () => {
    const saved = new Map(
      initialLocalSettings.map((setting) => [
        setting.key,
        `custom-${setting.key}`,
      ]),
    )
    for (const [key, value] of [
      ["password", "sentinel"],
      ["token", "sentinel"],
      ["reader-progress", "sentinel"],
      ["lang", "sentinel"],
      ["hope-ui-color-mode", "sentinel"],
    ])
      saved.set(key, value)
    const before = new Map(saved)
    const entries = localSettingResetEntries(group)
    assert.ok(entries.length)
    for (const [key, value] of entries) saved.set(key, value)
    for (const [key, value] of before) {
      const setting = initialLocalSettings.find((item) => item.key === key)
      assert.equal(
        saved.get(key),
        setting?.group === group && !setting.sensitive
          ? setting.default
          : value,
        key,
      )
    }
    assert.equal(saved.get("aria2_rpc_secret"), before.get("aria2_rpc_secret"))
  })
}

test("reset and numeric input contracts do not clear browser storage", async () => {
  const store = await readFile(
    new URL("../src/store/local_settings.ts", import.meta.url),
    "utf8",
  )
  const component = await readFile(
    new URL("../src/pages/home/toolbar/LocalSettings.tsx", import.meta.url),
    "utf8",
  )
  assert.doesNotMatch(store, /(?:localStorage\.)?clear\s*\(/)
  assert.match(store, /localSettingResetEntries\(group\)/)
  assert.match(store, /normalizeLocalNumber\(setting, value, local\[key\]\)/)
  assert.match(component, /value=\{draft\(\)\}/)
  assert.match(component, /onBlur=\{\(\) => commitNumber\(\)\}/)
  assert.match(component, /<details class="local-settings-advanced">/)
  assert.match(component, /checked=\{local\[props.key\] === "true"\}/)
})

test("toolbar contracts: one native button, retained menu refs and explicit labels", async () => {
  const icon = await readFile(
    new URL("../src/pages/home/toolbar/Icon.tsx", import.meta.url),
    "utf8",
  )
  const right = await readFile(
    new URL("../src/pages/home/toolbar/Right.tsx", import.meta.url),
    "utf8",
  )
  assert.equal((icon.match(/as="button"/g) ?? []).length, 1)
  assert.match(icon, /toolbar-action toolbar-action--/)
  assert.match(icon, /aria-label=\{label\(\)\}/)
  assert.match(icon, /title=\{props.title \?\? label\(\)\}/)
  assert.match(icon, /\.\.\.\(rest as IconProps<"button">\)/)
  assert.match(icon, /aria-hidden="true"/)
  assert.doesNotMatch(right, /event.key === "Enter"/)
  assert.match(right, /aria-expanded=\{false\}/)
  assert.match(right, /aria-expanded=\{true\}/)
  assert.match(right, /useReducedMotion\(\)/)
  assert.match(right, /duration: reducedMotion\(\) \? 0 : 0.2/)
})
