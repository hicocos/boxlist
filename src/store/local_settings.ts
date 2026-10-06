import { createLocalStorage } from "@solid-primitives/storage"
import { batch } from "solid-js"
import {
  initialLocalSettings,
  localSettingResetEntries,
  normalizeLocalNumber,
  type LocalSettingGroup,
} from "~/utils/local-settings-policy"

const [local, writeLocal, { remove, clear, toJSON }] = createLocalStorage()

/** All writers (including the editor toolbar) share the numeric guard. */
const setLocal: typeof writeLocal = (key, value, options) => {
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
export { local, setLocal, remove, clear, toJSON }
