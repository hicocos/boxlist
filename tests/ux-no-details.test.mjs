import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const folder = new URL("../src/pages/home/folder/", import.meta.url)

test("file layouts do not render redundant filename detail actions", async () => {
  for (const name of [
    "ListItem.tsx",
    "GridItem.tsx",
    "ImageItem.tsx",
    "FileName.tsx",
  ]) {
    const source = await readFile(new URL(name, folder), "utf8")
    assert.doesNotMatch(
      source,
      /FileNameDetails|ux-filename-details|ux-full-filename/,
    )
  }
  const filename = await readFile(new URL("FileName.tsx", folder), "utf8")
  assert.doesNotMatch(filename, /Modal|useUtil|createSignal/)
  assert.match(filename, /filenameParts/)
})

test("removed detail column leaves no reserved list or card padding", async () => {
  const css = await readFile(new URL("file-rows.css", folder), "utf8")
  const title = await readFile(new URL("List.tsx", folder), "utf8")
  assert.doesNotMatch(
    css,
    /ux-filename-details|ux-full-filename|padding-right:\s*(44|48)px/,
  )
  assert.doesNotMatch(title, /pr=\{props.readerEntry/)
})
