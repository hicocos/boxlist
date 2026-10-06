import { splitFileName } from "./file-names"

export interface FileRename {
  src_name: string
  new_name: string
}
export interface RenameSource {
  name: string
  is_dir: boolean
  created: string
  modified: string
}
export interface RenameOptions {
  type: string
  source: string
  replacement: string
  padding: string
}
export type RenameError =
  "empty" | "invalid" | "duplicate" | "conflict" | "missing"

export function renameOptionsError(
  options: RenameOptions,
): "source" | "regex" | "number" | "padding" | undefined {
  if (!options.source) return "source"
  if (options.type === "1") {
    try {
      new RegExp(options.source, "g")
    } catch {
      return "regex"
    }
  }
  if (options.type === "2") {
    if (
      !/^\d+$/.test(options.replacement) ||
      !Number.isSafeInteger(Number(options.replacement))
    )
      return "number"
    if (
      options.padding &&
      (!/^\d+$/.test(options.padding) || Number(options.padding) > 255)
    )
      return "padding"
  }
}

export function generateFileRenames(
  objects: readonly RenameSource[],
  options: RenameOptions,
): FileRename[] {
  if (renameOptionsError(options)) return []
  const { type, source, replacement } = options
  const regexp = type === "1" ? new RegExp(source, "g") : undefined
  let sequence = replacement
  return objects.flatMap((obj) => {
    let next: string
    if (type === "1") {
      regexp!.lastIndex = 0
      if (!regexp!.test(obj.name)) return []
      regexp!.lastIndex = 0
      next = obj.name.replace(regexp!, replacement)
      for (const key of ["created", "modified"] as const) {
        const date = new Date(obj[key])
        const values = {
          year: date.getFullYear(),
          month: date.getMonth() + 1,
          date: date.getDate(),
          hour: date.getHours(),
          minute: date.getMinutes(),
          second: date.getSeconds(),
        }
        for (const [part, value] of Object.entries(values)) {
          next = next.replaceAll(
            `{${key}_${part}}`,
            String(value).padStart(part === "year" ? 4 : 2, "0"),
          )
        }
      }
    } else if (type === "2") {
      const suffix = splitFileName(obj.name, obj.is_dir).extension
      const number = sequence.padStart(Number(options.padding) || 0, "0")
      next =
        (source.includes("{number}")
          ? source.replaceAll("{number}", number)
          : source + number) + suffix
      // BigInt avoids loss of identity when the sequence crosses MAX_SAFE_INTEGER.
      sequence = (BigInt(sequence) + 1n)
        .toString()
        .padStart(sequence.length, "0")
    } else {
      if (!obj.name.includes(source)) return []
      next = obj.name.replace(source, replacement)
    }
    return [{ src_name: obj.name, new_name: next }]
  })
}

/** Conservative case-insensitive conflicts are blocked on every storage provider. */
export function validateFileRenames(
  renames: readonly FileRename[],
  existingNames: readonly string[],
) {
  const key = (name: string) => name.normalize("NFC").toLowerCase()
  const existing = new Map<string, Set<string>>()
  for (const name of existingNames) {
    const names = existing.get(key(name)) ?? new Set<string>()
    names.add(name)
    existing.set(key(name), names)
  }
  const counts = new Map<string, number>()
  for (const item of renames)
    counts.set(key(item.new_name), (counts.get(key(item.new_name)) ?? 0) + 1)
  const rows = renames.map((item) => {
    const errors: RenameError[] = []
    const unchanged = item.src_name === item.new_name
    if (!item.new_name.trim()) errors.push("empty")
    if (
      item.new_name === "." ||
      item.new_name === ".." ||
      /[\/\\?<>*:|"\u0000-\u001f\u007f]/u.test(item.new_name)
    )
      errors.push("invalid")
    if ((counts.get(key(item.new_name)) ?? 0) > 1) errors.push("duplicate")
    if (!existingNames.includes(item.src_name)) errors.push("missing")
    // Also block cycles/chains into another source: backend processing may be sequential.
    if (
      !unchanged &&
      [...(existing.get(key(item.new_name)) ?? [])].some(
        (name) => name !== item.src_name,
      )
    )
      errors.push("conflict")
    return { ...item, unchanged, errors }
  })
  const changed = rows.filter((row) => !row.unchanged)
  const invalid = rows.some((row) => row.errors.length > 0)
  return {
    rows,
    unchanged: rows.filter((row) => row.unchanged).length,
    invalid,
    canSubmit: !invalid && changed.length > 0,
    changes: changed.map(({ src_name, new_name }) => ({ src_name, new_name })),
  }
}
