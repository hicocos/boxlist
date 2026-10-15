export const filenameOverflowModes = ["ellipsis", "scrollable", "multi_line"] as const
export type FilenameOverflow = (typeof filenameOverflowModes)[number]
export type FilenamePreference = FilenameOverflow | "inherit"

// This namespace performs a one-time cutover; do not change it on routine upgrades.
const prefix = "boxlist:filename-overflow:v2:"
const previousPrefix = "boxlist:filename-overflow:v1:"
const legacyKey = "list_item_filename_overflow"

export function isFilenameOverflow(value: unknown): value is FilenameOverflow {
  return filenameOverflowModes.some((mode) => mode === value)
}

export function filenamePreferenceKey(
  basePath: string,
  user: { id?: number; role?: number },
): string | null {
  if (!Number.isSafeInteger(user.id) || user.id! <= 0 || user.role == null) return null
  return prefix + JSON.stringify([basePath, user.role === 1 ? "guest" : `user:${user.id}`])
}

export function resolveFilenameOverflow(
  preference: unknown,
  siteDefault: unknown,
): FilenameOverflow {
  return isFilenameOverflow(preference)
    ? preference
    : isFilenameOverflow(siteDefault)
      ? siteDefault
      : "ellipsis"
}

type PreferenceRecord = { value: FilenamePreference }
type PreferenceStorage = Pick<Storage, "getItem" | "setItem" | "removeItem">

export function createFilenamePreferences(storage: PreferenceStorage) {
  const read = (key: string | null): PreferenceRecord => {
    if (key) {
      try {
        const record = JSON.parse(storage.getItem(key) ?? "null")
        if (record && (record.value === "inherit" || isFilenameOverflow(record.value))) {
          return { value: record.value }
        }
      } catch {
        // An invalid record must not prevent browsing or following the site default.
      }
    }
    return { value: "inherit" }
  }
  return {
    read,
    set(key: string | null, value: string) {
      if (!key || (value !== "inherit" && !isFilenameOverflow(value))) return
      storage.setItem(key, JSON.stringify({ value }))
    },
    migrate(key: string | null) {
      if (!key) return
      // Old browser-wide and account-scoped choices intentionally no longer apply.
      // New choices live in v2 and must never be cleared by later visits or logins.
      const previousKey = previousPrefix + key.slice(prefix.length)
      if (storage.getItem(previousKey) != null) storage.removeItem(previousKey)
      if (storage.getItem(legacyKey) != null) storage.removeItem(legacyKey)
    },
    isStorageKey(key: string | null) {
      return key == null || key === legacyKey || key.startsWith(prefix)
    },
  }
}
