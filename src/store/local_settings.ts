import { createLocalStorage } from "@solid-primitives/storage"
import { batch, createComputed, createRoot, createSignal, untrack } from "solid-js"
import {
  initialLocalSettings,
  localSettingResetEntries,
  normalizeLocalNumber,
  type LocalSettingGroup,
} from "~/utils/local-settings-policy"
import { base_path } from "~/utils/config"
import {
  createFilenamePreferences,
  filenamePreferenceKey,
  resolveFilenameOverflow,
  type FilenamePreference,
} from "~/utils/filename-preferences"
import { siteFilenameOverflow } from "./settings"
import { me } from "./user"

const [local, writeLocal, { remove, clear, toJSON }] = createLocalStorage()
const [filenameRevision, setFilenameRevision] = createSignal(0)
const [filenamePreference, setFilenamePreference] = createSignal<{
  value: FilenamePreference
}>({ value: "inherit" })
const filenamePreferences = createFilenamePreferences({
  getItem: (key) => local[key] ?? null,
  setItem: (key, value) => {
    writeLocal(key, value)
    setFilenameRevision((revision) => revision + 1)
  },
  removeItem: (key) => {
    remove(key)
    setFilenameRevision((revision) => revision + 1)
  },
})

export const getFilenameOverflow = () =>
  resolveFilenameOverflow(filenamePreference().value, siteFilenameOverflow())

// Wait for /me so each identity's old preference is retired independently.
queueMicrotask(() =>
  createRoot(() =>
    createComputed(() => {
      const key = filenamePreferenceKey(base_path, me())
      untrack(() => filenamePreferences.migrate(key))
      filenameRevision()
      setFilenamePreference(untrack(() => filenamePreferences.read(key)))
    }),
  ),
)
window.addEventListener("storage", (event) => {
  if (event.storageArea === localStorage && filenamePreferences.isStorageKey(event.key)) {
    setFilenameRevision((revision) => revision + 1)
  }
})

/** All writers (including the editor toolbar) share the numeric guard. */
const setLocal: typeof writeLocal = (key, value, options) => {
  if (key === "list_item_filename_overflow") {
    filenamePreferences.set(filenamePreferenceKey(base_path, me()), value)
    return
  }
  const setting = initialLocalSettings.find((entry) => entry.key === key)
  writeLocal(
    key,
    setting?.type === "number"
      ? normalizeLocalNumber(setting, value, local[key])
      : value,
    options,
  )
}

export function resetLocalSettingsGroup(group: LocalSettingGroup) {
  batch(() => {
    for (const [key, value] of localSettingResetEntries(group)) {
      setLocal(key, value)
    }
  })
}

for (const setting of initialLocalSettings) {
  if (setting.key === "list_item_filename_overflow") continue
  const saved = local[setting.key]
  if (saved == null) {
    setLocal(setting.key, setting.default)
  } else if (setting.type === "number") {
    const valid = normalizeLocalNumber(setting, saved)
    if (valid !== saved) setLocal(setting.key, valid)
  }
}

export { initialLocalSettings } from "~/utils/local-settings-policy"
export type {
  LocalSetting,
  LocalSettingGroup,
} from "~/utils/local-settings-policy"
export { local, setLocal, remove, clear, toJSON, filenamePreference }
