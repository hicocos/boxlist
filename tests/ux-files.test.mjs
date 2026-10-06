import assert from "node:assert/strict"
import { test } from "node:test"
import { readFile } from "node:fs/promises"
import ts from "typescript"

// Transpile only the actual pure production modules; no browser/API/auth mocks.
const modules = new Map()
async function moduleUrl(name) {
  if (modules.has(name)) return modules.get(name)
  let source = await readFile(
    new URL(`../src/utils/${name}.ts`, import.meta.url),
    "utf8",
  )
  for (const match of [...source.matchAll(/from "\.\/(file-[^"]+)"/g)]) {
    source = source.replace(match[0], `from "${await moduleUrl(match[1])}"`)
  }
  const output = ts.transpileModule(source, {
    compilerOptions: {
      target: ts.ScriptTarget.ES2022,
      module: ts.ModuleKind.ESNext,
    },
  }).outputText
  const url = `data:text/javascript;base64,${Buffer.from(output).toString("base64")}`
  modules.set(name, url)
  return url
}
const {
  changeSelection,
  emptySelectionState,
  visibleSelection,
  nativeLinkClick,
} = await import(await moduleUrl("file-selection"))
const { filenameParts, splitFileName, changedNameParts } = await import(
  await moduleUrl("file-names")
)
const { generateFileRenames, renameOptionsError, validateFileRenames } =
  await import(await moduleUrl("file-renames"))
const { parseFileSortState } = await import(await moduleUrl("file-sort"))
const a = { name: "a.jpg" },
  hidden = { name: "README.md" },
  folder = { name: "folder" },
  b = { name: "b.jpg" },
  c = { name: "c.jpg" }

test("filtered directory identity never selects the image at filtered index zero", () => {
  const loaded = [a, hidden, folder, b]
  const result = changeSelection(
    visibleSelection(loaded, [folder, a, b]),
    new Set(),
    emptySelectionState(),
    folder,
    true,
  )
  assert.deepEqual([...result.selected], [folder])
  assert.equal(loaded.indexOf(folder), 2)
})
test("selectable scope contains only unique visible loaded objects", () => {
  assert.deepEqual(
    visibleSelection([a, hidden, folder, b], [folder, a, b, b, c]),
    [folder, a, b],
  )
})
test("Shift without an anchor selects only the requested visible object", () => {
  const result = changeSelection(
    [a, b],
    new Set(),
    emptySelectionState(),
    b,
    true,
    { range: true },
  )
  assert.deepEqual([...result.selected], [b])
})
test("Shift range excludes hidden objects and follows display order", () => {
  let result = changeSelection(
    [folder, a, b],
    new Set([hidden]),
    emptySelectionState(),
    folder,
    true,
  )
  result = changeSelection(
    [folder, a, b],
    result.selected,
    result.state,
    b,
    true,
    { range: true },
  )
  assert.deepEqual([...result.selected], [folder, a, b])
})
test("shrinking a Shift range preserves unrelated prior selections", () => {
  let result = changeSelection(
    [folder, a, b, c],
    new Set([folder]),
    emptySelectionState(),
    a,
    true,
  )
  result = changeSelection(
    [folder, a, b, c],
    result.selected,
    result.state,
    c,
    true,
    { range: true },
  )
  result = changeSelection(
    [folder, a, b, c],
    result.selected,
    result.state,
    b,
    true,
    { range: true },
  )
  assert.deepEqual([...result.selected], [folder, a, b])
})
test("sorting retains the anchor by object identity", () => {
  let result = changeSelection(
    [a, b, c],
    new Set(),
    emptySelectionState(),
    a,
    true,
  )
  result = changeSelection([c, b, a], result.selected, result.state, b, true, {
    range: true,
  })
  assert.deepEqual(new Set(result.selected), new Set([a, b]))
})
test("hidden, stale, replaced and unloaded targets are never selected", () => {
  for (const target of [hidden, c, { name: "a.jpg" }]) {
    const result = changeSelection(
      [a, b],
      new Set([a]),
      emptySelectionState(),
      target,
      true,
    )
    assert.deepEqual([...result.selected], [a])
  }
})
test("single context target ignores Shift range", () => {
  const result = changeSelection(
    [a, b, c],
    new Set([a, b]),
    { anchor: a, baseline: null },
    c,
    true,
    { one: true, range: true },
  )
  assert.deepEqual([...result.selected], [c])
})
test("keyboard/AT and modified clicks retain native link semantics", () => {
  const pointer = {
    button: 0,
    detail: 1,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
  }
  assert.equal(nativeLinkClick(pointer), false)
  for (const override of [
    { detail: 0 },
    { button: 1 },
    { ctrlKey: true },
    { metaKey: true },
    { altKey: true },
  ])
    assert.equal(nativeLinkClick({ ...pointer, ...override }), true)
})
test("ellipsis preserves extension, unicode and complete textual identity", () => {
  for (const name of [
    "短名.txt",
    ".env",
    "README",
    "name.",
    "这是非常长的中文文件名".repeat(5) + ".pdf",
    "🌍".repeat(40) + ".webp",
  ]) {
    const parts = filenameParts(name)
    assert.equal(parts.start + parts.end, name)
  }
  assert.equal(
    filenameParts("a".repeat(60) + ".jpeg").end.endsWith(".jpeg"),
    true,
  )
  assert.equal(splitFileName("dir.with.dots", true).extension, "")
  assert.equal(splitFileName(".env").extension, "")
})
test("change highlighting reconstructs both names, including unicode", () => {
  for (const [before, after] of [
    ["abc.txt", "aXYZ.txt"],
    ["foo", ""],
    ["", "x"],
    ["same", "same"],
    ["📗资料.txt", "📘资料.txt"],
  ]) {
    const d = changedNameParts(before, after)
    assert.equal(d.prefix + d.removed + d.suffix, before)
    assert.equal(d.prefix + d.added + d.suffix, after)
  }
})
const obj = (name, is_dir = false) => ({
  name,
  is_dir,
  created: "2024-01-02T03:04:05",
  modified: "2025-02-03T04:05:06",
})
const options = (type, source, replacement, padding = "") => ({
  type,
  source,
  replacement,
  padding,
})
test("regex captures and created/modified tokens remain supported", () => {
  const result = generateFileRenames(
    [obj("old-12.txt")],
    options("1", "old-(\\d+)", "new-$1-{created_year}-{modified_month}"),
  )
  assert.equal(result[0].new_name, "new-12-2024-02.txt")
})
test("literal replacement can remove a fragment without rejecting empty replacement", () => {
  const result = generateFileRenames(
    [obj("old-file.txt")],
    options("3", "old-", ""),
  )
  assert.equal(result[0].new_name, "file.txt")
  assert.equal(validateFileRenames(result, ["old-file.txt"]).canSubmit, true)
})
test("sequential rename pads numbers, retains file suffixes, not directory suffixes", () => {
  const result = generateFileRenames(
    [obj("a.txt"), obj("dir.part", true), obj(".env")],
    options("2", "item-{number}", "09", "3"),
  )
  assert.deepEqual(
    result.map((r) => r.new_name),
    ["item-009.txt", "item-010", "item-011"],
  )
})
test("bad regex, fractional or absent number, excessive padding cannot generate", () => {
  for (const config of [
    options("1", "[", "x"),
    options("2", "file", ""),
    options("2", "file", "1.5"),
    options("2", "file", "NaN"),
    options("2", "file", "1", "999999"),
    options("3", "", "x"),
  ]) {
    assert.ok(renameOptionsError(config))
    assert.deepEqual(generateFileRenames([obj("x")], config), [])
  }
})
test("generated empty, dot, separator and control-character names are blocked", () => {
  for (const name of [
    "",
    " ",
    ".",
    "..",
    "a/b",
    "a\\b",
    "a\0b",
    "a\nb",
    "a?b",
  ]) {
    const result = validateFileRenames(
      [{ src_name: "a", new_name: name }],
      ["a"],
    )
    assert.equal(result.canSubmit, false, JSON.stringify(name))
  }
})
test("regex output is validated, not just the replacement pattern", () => {
  const generated = generateFileRenames(
    [obj("a.txt")],
    options("1", ".*", "../$&"),
  )
  assert.equal(validateFileRenames(generated, ["a.txt"]).canSubmit, false)
})
test("duplicate destinations are blocked on every involved row", () => {
  const result = validateFileRenames(
    [
      { src_name: "a", new_name: "new" },
      { src_name: "b", new_name: "NEW" },
    ],
    ["a", "b"],
  )
  assert.equal(result.canSubmit, false)
  assert.ok(result.rows.every((row) => row.errors.includes("duplicate")))
})
test("existing hidden names and rename cycles are conservatively blocked", () => {
  assert.equal(
    validateFileRenames(
      [{ src_name: "a", new_name: "README.md" }],
      ["a", "README.md"],
    ).canSubmit,
    false,
  )
  assert.equal(
    validateFileRenames(
      [
        { src_name: "a", new_name: "b" },
        { src_name: "b", new_name: "a" },
      ],
      ["a", "b"],
    ).canSubmit,
    false,
  )
})
test("unchanged items are shown but excluded from the API payload", () => {
  const result = validateFileRenames(
    [
      { src_name: "a", new_name: "a" },
      { src_name: "b", new_name: "c" },
    ],
    ["a", "b"],
  )
  assert.equal(result.canSubmit, true)
  assert.equal(result.unchanged, 1)
  assert.deepEqual(result.changes, [{ src_name: "b", new_name: "c" }])
  assert.equal(
    validateFileRenames([{ src_name: "a", new_name: "a" }], ["a"]).canSubmit,
    false,
  )
  assert.equal(validateFileRenames([], []).canSubmit, false)
})
test("missing sources cannot submit after a refresh", () => {
  assert.equal(
    validateFileRenames([{ src_name: "a", new_name: "c" }], ["b"]).canSubmit,
    false,
  )
})
test("persisted sort accepts existing valid format but rejects corrupt values", () => {
  assert.deepEqual(parseFileSortState('{"orderBy":"name","reverse":true}'), {
    orderBy: "name",
    reverse: true,
  })
  for (const value of [
    null,
    "{}",
    "null",
    "bad",
    '{"orderBy":"__proto__","reverse":true}',
    '{"orderBy":"size","reverse":"false"}',
  ])
    assert.equal(parseFileSortState(value), null)
})
