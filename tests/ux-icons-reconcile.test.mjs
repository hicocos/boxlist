import test from "node:test"
import assert from "node:assert/strict"
import {
  ICON_TARGETS,
  defaultIconConfig,
  canonicalIconConfig,
  reconcileIconDraft,
  persistIconConfig,
  readIconConfig,
  writeIconConfig,
} from "../src/utils/icon-policy.ts"
const config = (selections = {}) => ({ version: 1, selections })
test("reconciliation keeps local edits and unrelated edits by other pages through a verified retry", async () => {
  const baseline = defaultIconConfig()
  const local = config({ folder: "pack-folder" })
  const remote = config({ audio: "pack-music" })
  const copies = [baseline, local, remote].map(canonicalIconConfig)
  const merged = reconcileIconDraft(local, baseline, remote)
  assert.deepEqual(merged.conflicts, [])
  assert.deepEqual(
    merged.draft,
    config({ folder: "pack-folder", audio: "pack-music" }),
  )
  assert.deepEqual([baseline, local, remote].map(canonicalIconConfig), copies)
  let stored = {
    value: writeIconConfig('<meta name="keep" content="unchanged">', remote),
  }
  let writes = 0
  const result = await persistIconConfig(
    {
      read: async () => ({ ...stored }),
      save: async (item) => {
        writes++
        stored = item
      },
    },
    merged.draft,
    remote,
  )
  assert.equal(writes, 1)
  assert.deepEqual(result, merged.draft)
  assert.deepEqual(readIconConfig(stored.value), merged.draft)
  assert.ok(stored.value.includes('<meta name="keep" content="unchanged">'))
})
test("same-target divergent edits are conflicts, not silently approved overwrites", () => {
  const baseline = config({ folder: "pack-folder", audio: "pack-music" })
  const local = config({ folder: "legacy-smile", audio: "pack-music" })
  const remote = config({ folder: "pack-doc", audio: "pack-wav" })
  const merged = reconcileIconDraft(local, baseline, remote)
  assert.deepEqual(merged.conflicts, ["folder"])
  assert.deepEqual(
    merged.draft,
    config({ folder: "legacy-smile", audio: "pack-wav" }),
  )
})
test("deletions, explicit native defaults, identical commits and remote-only changes rebase correctly", () => {
  const baseline = config({
    folder: "pack-folder",
    audio: "pack-music",
    "ext:mp3": "pack-mp3",
  })
  const local = config({ audio: "pack-music", "ext:mp3": "default" })
  const remote = config({
    folder: "pack-folder",
    audio: "pack-wav",
    "ext:mp3": "pack-mp3",
    image: "pack-image",
  })
  const result = reconcileIconDraft(local, baseline, remote)
  assert.deepEqual(result.conflicts, [])
  assert.deepEqual(
    result.draft,
    config({ audio: "pack-wav", "ext:mp3": "default", image: "pack-image" }),
  )
  assert.deepEqual(reconcileIconDraft(local, baseline, local), {
    draft: local,
    conflicts: [],
  })
  assert.deepEqual(reconcileIconDraft(baseline, baseline, remote), {
    draft: remote,
    conflicts: [],
  })
  assert.deepEqual(
    reconcileIconDraft(local, baseline, config({ folder: "legacy-smile" }))
      .conflicts,
    ["folder", "ext:mp3"],
  )
})
test("every target obeys the same three-way merge rule for absence, default and custom choices", () => {
  const values = [undefined, "default", "pack-folder", "legacy-smile"]
  for (const target of ICON_TARGETS)
    for (const before of values)
      for (const mine of values)
        for (const server of values) {
          const make = (value) =>
            config(value === undefined ? {} : { [target.id]: value })
          const result = reconcileIconDraft(
            make(mine),
            make(before),
            make(server),
          )
          const changed = mine !== before
          assert.equal(
            result.draft.selections[target.id],
            changed ? mine : server,
            target.id,
          )
          assert.deepEqual(
            result.conflicts,
            changed && server !== before && server !== mine ? [target.id] : [],
          )
        }
})
