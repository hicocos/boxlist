import test from "node:test"
import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"

test("integrated icon API and previews use the same-origin /icon namespace", async () => {
 const upload = await readFile(new URL("../src/pages/manage/icons/icon-upload.ts", import.meta.url), "utf8")
 const assets = await readFile(new URL("../src/utils/icon-assets.ts", import.meta.url), "utf8")
 assert.match(upload, /base_path\}\/icon\/api/)
 assert.match(assets, /base_path\}\/icon\/assets/)
 assert.doesNotMatch(upload + assets, /\/icon-library\//)
})

test("service check never sends auth, follows redirects or accepts arbitrary endpoints", async () => {
 const source = await readFile(new URL("../src/pages/manage/icons/IconServiceStatus.tsx", import.meta.url), "utf8")
 assert.match(source, /base_path\}\/icon\/api\/health/)
 assert.match(source, /credentials: "omit"/)
 assert.match(source, /redirect: "error"/)
 assert.match(source, /AbortController/)
 assert.match(source, /application\/json/)
 assert.doesNotMatch(source, /Authorization|localStorage|token\s*[:=(]|type="url"/)
})
