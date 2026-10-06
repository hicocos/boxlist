// Pure icon policy: safe IDs only, no URLs or executable configuration.
export const ICON_CONFIG_ID = "openlist-icon-config"
export const ICON_CONFIG_EVENT = "openlist-icons-changed"
export const DEFAULT_ICON = "default"
export type IconConfig = { version: 1; selections: Record<string, string> }
export type IconTarget = {
  id: string
  label: string
  english: string
  group: string
  extensions?: string[]
  type: number
  example: string
  recommended?: string
}

const baseTargets: IconTarget[] = [
  {
    id: "folder",
    label: "文件夹",
    english: "Folders",
    group: "general",
    type: 1,
    example: "示例文件夹",
    recommended: "pack-folder",
  },
  {
    id: "video",
    label: "视频（通用）",
    english: "Video fallback",
    group: "general",
    type: 2,
    example: "示例视频.mp4",
    recommended: "pack-video",
  },
  {
    id: "audio",
    label: "音频（通用）",
    english: "Audio fallback",
    group: "general",
    type: 3,
    example: "示例音乐.flac",
    recommended: "pack-music",
  },
  {
    id: "image",
    label: "图片（通用）",
    english: "Image fallback",
    group: "general",
    type: 5,
    example: "示例图片.jpg",
    recommended: "pack-image",
  },
  {
    id: "text",
    label: "文本 / 代码（通用）",
    english: "Text / code fallback",
    group: "general",
    type: 4,
    example: "示例代码.py",
    recommended: "pack-document",
  },
  {
    id: "archive",
    label: "压缩包（通用）",
    english: "Archive fallback",
    group: "general",
    type: 0,
    example: "示例压缩包.7z",
  },
  {
    id: "other",
    label: "其他文件",
    english: "Other files",
    group: "general",
    type: 0,
    example: "示例文件.bin",
  },
  {
    id: "download",
    label: "下载操作",
    english: "Download action",
    group: "general",
    type: 0,
    example: "下载按钮",
    recommended: "pack-download",
  },
]
const formats: Array<[string, string, number, string?]> = [
  ["doc", "office", 0, "pack-doc"],
  ["docx", "office", 0, "pack-docx"],
  ["xls", "office", 0],
  ["xlsx", "office", 0, "pack-xlsx"],
  ["ppt", "office", 0, "pack-ppt"],
  ["pptx", "office", 0, "pack-pptx"],
  ["pdf", "office", 0, "pack-pdf"],
  ["psd", "office", 0, "pack-psd"],
  ["ai", "office", 0],
  ["epub", "office", 0],
  ["mp3", "audio", 3, "pack-mp3"],
  ["wav", "audio", 3, "pack-wav"],
  ["wma", "audio", 3, "pack-wma"],
  ["flac", "audio", 3],
  ["ogg", "audio", 3],
  ["m4a", "audio", 3],
  ["opus", "audio", 3],
  ["aac", "audio", 3],
  ["mp4", "video", 2],
  ["mkv", "video", 2],
  ["avi", "video", 2],
  ["mov", "video", 2],
  ["rmvb", "video", 2],
  ["webm", "video", 2],
  ["flv", "video", 2],
  ["m3u8", "video", 2],
  ["jpg", "image", 5],
  ["jpeg", "image", 5],
  ["png", "image", 5],
  ["gif", "image", 5],
  ["webp", "image", 5],
  ["avif", "image", 5],
  ["svg", "image", 5],
  ["bmp", "image", 5],
  ["tiff", "image", 5],
  ["ico", "image", 5],
  ["heic", "image", 5],
  ["heif", "image", 5],
  ["zip", "archive", 0, "pack-zip"],
  ["rar", "archive", 0, "pack-rar"],
  ["cab", "archive", 0, "pack-cab"],
  ["7z", "archive", 0],
  ["tar", "archive", 0],
  ["gz", "archive", 0],
  ["bz2", "archive", 0],
  ["xz", "archive", 0],
  ["tgz", "archive", 0],
  ["zst", "archive", 0],
  ["txt", "text", 4, "pack-txt"],
  ["md", "text", 4],
  ["json", "text", 4],
  ["html", "text", 4],
  ["xml", "text", 4],
  ["js", "text", 4],
  ["ts", "text", 4],
  ["py", "text", 4],
  ["yml", "text", 4],
  ["ini", "text", 4],
  ["srt", "text", 4],
  ["lrc", "text", 4],
  ["dmg", "special", 0],
  ["ipa", "special", 0],
  ["plist", "special", 0],
  ["tipa", "special", 0],
  ["exe", "special", 0],
  ["msi", "special", 0],
  ["apk", "special", 0],
  ["db", "special", 0],
  ["iso", "special", 0],
  ["url", "special", 0],
  ["cast", "special", 0],
  ...[
    "htm",
    "java",
    "properties",
    "sql",
    "conf",
    "vue",
    "php",
    "bat",
    "gitignore",
    "go",
    "sh",
    "c",
    "cpp",
    "h",
    "hpp",
    "tsx",
    "vtt",
    "ass",
    "rs",
    "strm",
  ].map((extension) => [extension, "text", 4] as [string, string, number]),
  ["swf", "image", 5],
  ...[
    "tlz4",
    "tbz2",
    "lz4",
    "sz",
    "s2",
    "zz",
    "mz",
    "livp",
    "lz",
    "tlz",
    "txz",
    "tzst",
    "br",
  ].map((extension) => [extension, "archive", 0] as [string, string, number]),
]
export const ICON_TARGETS: IconTarget[] = [
  ...baseTargets,
  ...formats.map(([extension, group, type, recommended]) => ({
    id: `ext:${extension}`,
    label: extension.toUpperCase(),
    english: extension.toUpperCase(),
    extensions: [extension],
    group,
    type,
    example: `示例文件.${extension}`,
    recommended,
  })),
]
export const ICON_ASSET_IDS = new Set([
  "pack-cab",
  "pack-doc",
  "pack-docx",
  "pack-mp3",
  "pack-pdf",
  "pack-ppt",
  "pack-pptx",
  "pack-psd",
  "pack-rar",
  "pack-txt",
  "pack-wav",
  "pack-wma",
  "pack-xlsx",
  "pack-zip",
  "pack-download",
  "pack-image",
  "pack-folder",
  "pack-document",
  "pack-video",
  "pack-music",
  "legacy-smile",
])
// Uploaded IDs are immutable, path-safe server-generated identifiers, never URLs.
export const isUploadedIconId = (value: unknown): value is string =>
  typeof value === "string" && /^upload-[a-f0-9]{32}$/.test(value)
export const isKnownIconAssetId = (value: unknown): value is string =>
  typeof value === "string" &&
  (ICON_ASSET_IDS.has(value) || isUploadedIconId(value))
export type UploadedIconAsset = {
  id: string
  name: string
  width: number
  height: number
  bytes: number
  created_at: string
}
export const parseUploadedIconAsset = (raw: unknown): UploadedIconAsset => {
  if (!raw || typeof raw !== "object" || Array.isArray(raw))
    throw new Error("上传素材信息无效")
  const item = raw as UploadedIconAsset
  if (
    !isUploadedIconId(item.id) ||
    typeof item.name !== "string" ||
    !item.name.trim() ||
    item.name.length > 160 ||
    /[\u0000-\u001f\u007f]/.test(item.name) ||
    item.width !== 256 ||
    item.height !== 256 ||
    !Number.isInteger(item.bytes) ||
    item.bytes < 1 ||
    item.bytes > 5 * 1024 * 1024 ||
    typeof item.created_at !== "string" ||
    !item.created_at ||
    item.created_at.length > 64
  )
    throw new Error("上传素材信息无效")
  return {
    id: item.id,
    name: item.name,
    width: item.width,
    height: item.height,
    bytes: item.bytes,
    created_at: item.created_at,
  }
}
export const parseUploadedIconCatalog = (raw: unknown): UploadedIconAsset[] => {
  if (!Array.isArray(raw) || raw.length > 200)
    throw new Error("上传素材列表无效")
  const rows = raw.map(parseUploadedIconAsset)
  if (new Set(rows.map((item) => item.id)).size !== rows.length)
    throw new Error("上传素材列表重复")
  return rows
}
export type IconLibraryCatalog = {
  assets: UploadedIconAsset[]
  hidden_builtins: string[]
}
export const parseIconLibraryCatalog = (raw: unknown): IconLibraryCatalog => {
  if (!raw || typeof raw !== "object" || Array.isArray(raw))
    throw new Error("素材库信息无效")
  const data = raw as {
    assets?: unknown
    hidden_builtins?: unknown
    version?: number
  }
  if (
    data.version !== 1 ||
    !Array.isArray(data.hidden_builtins) ||
    data.hidden_builtins.length > ICON_ASSET_IDS.size ||
    data.hidden_builtins.some(
      (id) => typeof id !== "string" || !ICON_ASSET_IDS.has(id),
    ) ||
    new Set(data.hidden_builtins).size !== data.hidden_builtins.length
  )
    throw new Error("素材库信息无效")
  return {
    assets: parseUploadedIconCatalog(data.assets),
    hidden_builtins: [...data.hidden_builtins],
  }
}
export const iconUploadFileError = (file: {
  name: string
  size: number
}): string => {
  if (!/\.png$/i.test(file.name)) return "只允许上传 PNG 格式，不支持动画 PNG。"
  if (
    !Number.isFinite(file.size) ||
    !Number.isInteger(file.size) ||
    file.size <= 0 ||
    file.size > 5 * 1024 * 1024
  )
    return "每张图标大小须在 1 字节至 5 MB 之间。"
  return ""
}
const knownTargets = new Set(ICON_TARGETS.map((target) => target.id))
const extensionTargets = new Map(
  ICON_TARGETS.flatMap((target) =>
    (target.extensions ?? []).map(
      (extension) => [extension, target.id] as const,
    ),
  ),
)
export const defaultIconConfig = (): IconConfig => ({
  version: 1,
  selections: {},
})
export const normalizeIconConfig = (raw: unknown): IconConfig => {
  const clean = defaultIconConfig()
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return clean
  const input = raw as { version?: unknown; selections?: unknown }
  if (
    input.version !== 1 ||
    !input.selections ||
    typeof input.selections !== "object" ||
    Array.isArray(input.selections)
  )
    return clean
  for (const [target, asset] of Object.entries(input.selections)) {
    if (
      knownTargets.has(target) &&
      typeof asset === "string" &&
      (asset === DEFAULT_ICON || isKnownIconAssetId(asset))
    ) {
      clean.selections[target] = asset
    }
  }
  return clean
}
export const strictIconConfig = (raw: unknown): IconConfig => {
  const clean = normalizeIconConfig(raw)
  const input = raw as IconConfig | undefined
  if (
    !input ||
    input.version !== 1 ||
    !input.selections ||
    typeof input.selections !== "object" ||
    Array.isArray(input.selections) ||
    Object.entries(input.selections).some(
      ([key, value]) => clean.selections[key] !== value,
    )
  ) {
    throw new Error("图标配置无效，请重新选择。")
  }
  return clean
}
// Syntax-safe IDs alone do not prove that an asset is still selectable.
// Call with a fresh catalog while holding the shared head write lock.
export const assertIconAssetsAvailable = (
  config: IconConfig,
  catalog: IconLibraryCatalog,
): void => {
  const safe = strictIconConfig(config)
  const uploaded = new Set(catalog.assets.map((asset) => asset.id))
  const hidden = new Set(catalog.hidden_builtins)
  const unavailable = ICON_TARGETS.filter((target) => {
    const id = safe.selections[target.id]
    return (
      id &&
      id !== DEFAULT_ICON &&
      (isUploadedIconId(id) ? !uploaded.has(id) : hidden.has(id))
    )
  })
  if (unavailable.length) {
    const error = new Error(
      `以下类型引用的素材已删除，请刷新素材并重新选择：${unavailable.map((target) => target.label).join("、")}`,
    )
    error.name = "IconAssetUnavailable"
    throw error
  }
}
export const canonicalIconConfig = (raw: unknown): string => {
  const config = normalizeIconConfig(raw)
  return JSON.stringify({
    version: 1,
    selections: Object.fromEntries(
      Object.entries(config.selections).sort(([a], [b]) => a.localeCompare(b)),
    ),
  })
}
export const recommendedIconConfig = (): IconConfig => ({
  version: 1,
  selections: Object.fromEntries(
    ICON_TARGETS.filter((target) => target.recommended).map((target) => [
      target.id,
      target.recommended!,
    ]),
  ),
})
export const iconTargetId = (type: number, name: string): string => {
  if (type === 1) return "folder"
  const dot = name.lastIndexOf(".")
  const extension = dot >= 0 ? name.slice(dot + 1).toLowerCase() : ""
  return extensionTargets.get(extension) ?? "other"
}
export const iconCategory = (
  type: number,
  name: string,
  archive = false,
): string => {
  if (type === 1) return "folder"
  const target = ICON_TARGETS.find(
    (item) => item.id === iconTargetId(type, name),
  )
  // ISO has its own native disc icon and is not an archive fallback.
  if (target?.group === "archive" || (archive && target?.id !== "ext:iso"))
    return "archive"
  if (target && ["video", "audio", "image", "text"].includes(target.group))
    return target.group
  if (type === 2) return "video"
  if (type === 3) return "audio"
  if (type === 4) return "text"
  if (type === 5) return "image"
  return "other"
}
export const resolveIconAsset = (
  config: IconConfig,
  type: number,
  name: string,
  archive = false,
): string | undefined => {
  const category = iconCategory(type, name, archive)
  const extensionTarget = iconTargetId(type, name)
  const target = extensionTarget === "other" ? category : extensionTarget
  if (Object.prototype.hasOwnProperty.call(config.selections, target)) {
    const selected = config.selections[target]
    return selected === DEFAULT_ICON ? undefined : selected
  }
  // A special native format remains native unless explicitly customized.
  const format = ICON_TARGETS.find((item) => item.id === target)
  if (
    format &&
    (format.group === "office" ||
      format.group === "special" ||
      target === "ext:md")
  )
    return undefined
  const asset = config.selections[category]
  return asset && asset !== DEFAULT_ICON ? asset : undefined
}

export const previewIconAsset = (
  config: IconConfig,
  target: IconTarget,
): string | undefined => {
  if (!target.id.startsWith("ext:")) {
    const selected = config.selections[target.id]
    return selected === DEFAULT_ICON ? undefined : selected
  }
  return resolveIconAsset(
    config,
    target.type,
    target.example,
    target.group === "archive",
  )
}

export const iconCanInherit = (target: IconTarget) =>
  target.id.startsWith("ext:") &&
  target.group !== "office" &&
  target.group !== "special" &&
  target.id !== "ext:md"
export const iconSelectionValue = (config: IconConfig, target: IconTarget) =>
  config.selections[target.id] ??
  (iconCanInherit(target) ? "inherit" : DEFAULT_ICON)
export const selectIconForTarget = (
  config: IconConfig,
  target: IconTarget,
  value: string,
): IconConfig => {
  if (
    value !== "inherit" &&
    value !== DEFAULT_ICON &&
    !isKnownIconAssetId(value)
  )
    throw new Error("Unknown icon asset")
  if (value === "inherit" && !iconCanInherit(target))
    throw new Error("This type cannot inherit a category")
  const selections = { ...config.selections }
  if (
    value === "inherit" ||
    (value === DEFAULT_ICON && !iconCanInherit(target))
  )
    delete selections[target.id]
  else selections[target.id] = value
  return { version: 1, selections }
}
export const changedIconTargetIds = (draft: IconConfig, baseline: IconConfig) =>
  ICON_TARGETS.filter(
    (target) => draft.selections[target.id] !== baseline.selections[target.id],
  ).map((target) => target.id)
// Rebase only edits made by this draft onto the latest saved configuration.
// Divergent changes to the same target must be explicitly resolved by the user.
export const reconcileIconDraft = (
  draft: IconConfig,
  baseline: IconConfig,
  saved: IconConfig,
): { draft: IconConfig; conflicts: string[] } => {
  const local = strictIconConfig(draft)
  const base = strictIconConfig(baseline)
  const latest = strictIconConfig(saved)
  const selections = { ...latest.selections }
  const conflicts: string[] = []
  for (const target of ICON_TARGETS) {
    const id = target.id
    const mine = local.selections[id]
    const before = base.selections[id]
    const server = latest.selections[id]
    if (mine === before) continue
    if (server !== before && server !== mine) conflicts.push(id)
    if (mine === undefined) delete selections[id]
    else selections[id] = mine
  }
  return { draft: { version: 1, selections }, conflicts }
}
export const filterIconManagerTargets = (
  group: string,
  query: string,
  onlyIds?: string[],
): IconTarget[] => {
  const keyword = query.trim().toLowerCase().replace(/^\./, "")
  const aliases: Record<string, string> = {
    general: "通用 general",
    office: "文档 文書 设计 word excel powerpoint document design",
    audio: "音频 音樂 音乐 audio music",
    video: "视频 影片 video movie",
    image: "图片 圖片 image photo",
    archive: "压缩包 壓縮 archive compression",
    text: "文本 文字 代码 代碼 text code",
    special: "应用 應用 其他 app special",
  }
  return ICON_TARGETS.filter((target) => {
    if (onlyIds && !onlyIds.includes(target.id)) return false
    // Nonblank searches always cover all types, regardless of the dropdown.
    if (keyword)
      return `${target.label} ${target.english} ${target.extensions?.join(" ") ?? ""} ${aliases[target.group] ?? ""}`
        .toLowerCase()
        .includes(keyword)
    if (group === "all") return true
    if (group === "common")
      return !target.id.startsWith("ext:") || !!target.recommended
    return target.group === group
  })
}

const iconBlock =
  /(?:\r?\n)?<!-- OPENLIST-ICON-THEME-START -->[\s\S]*?<!-- OPENLIST-ICON-THEME-END -->/g
const legacyBlock =
  /(?:\r?\n)?<!-- OPENLIST-FOLDER-ICON-START -->[\s\S]*?<!-- OPENLIST-FOLDER-ICON-END -->/g
export const readIconConfig = (head: string): IconConfig => {
  const owned = head.match(iconBlock)?.at(-1)
  if (owned) {
    const content = owned.match(
      /<meta\s+id="openlist-icon-config"\s+content="([^"]*)"\s*\/?\s*>/,
    )?.[1]
    try {
      return normalizeIconConfig(JSON.parse(decodeURIComponent(content ?? "")))
    } catch {
      return defaultIconConfig()
    }
  }
  const old = head.match(legacyBlock)?.at(-1)
  return old?.includes('"smile"')
    ? { version: 1, selections: { folder: "legacy-smile" } }
    : defaultIconConfig()
}
export const writeIconConfig = (head: string, raw: unknown): string => {
  const config = strictIconConfig(raw)
  const content = encodeURIComponent(canonicalIconConfig(config))
  const block = `<!-- OPENLIST-ICON-THEME-START -->\n<meta id="${ICON_CONFIG_ID}" content="${content}">\n<!-- OPENLIST-ICON-THEME-END -->`
  return (
    head.replace(iconBlock, "").replace(legacyBlock, "").trimEnd() +
    "\n" +
    block
  )
}
// Shared by the custom-head editor; preserve latest owned settings only.
const managedBlocks =
  /(?:\r?\n)?<!-- OPENLIST-(HOME-GLASS|FOLDER-ICON|ICON-THEME)-START -->[\s\S]*?<!-- OPENLIST-\1-END -->/g
export const editableManagedHead = (head: string) =>
  head.replace(managedBlocks, "")
export const mergeIconManagedHead = (editable: string, latest: string) => {
  const blocks = (latest.match(managedBlocks) ?? []).map((block) =>
    block.replace(/^\r?\n/, ""),
  )
  return (
    editableManagedHead(editable) +
    (blocks.length ? "\n" + blocks.join("\n") : "")
  )
}

export type IconSettingsTransport<T extends { value: string }> = {
  read: () => Promise<T>
  save: (item: T) => Promise<void>
  validate?: (config: IconConfig) => Promise<void>
}
// Preserve unrelated HTML and reject stale icon drafts. A successful save
// is not sufficient: read back the exact stored head before reporting success.
export const persistIconConfig = async <T extends { value: string }>(
  transport: IconSettingsTransport<T>,
  draft: IconConfig,
  baseline: IconConfig,
): Promise<IconConfig> => {
  const safe = strictIconConfig(draft)
  const latest = await transport.read()
  const current = canonicalIconConfig(readIconConfig(latest.value))
  const desired = canonicalIconConfig(safe)
  if (current !== desired && current !== canonicalIconConfig(baseline)) {
    const error = new Error("另一页面已修改图标配置，请先核对已保存配置。")
    error.name = "IconConfigConflict"
    throw error
  }
  // Validate even an idempotent confirmation, so a deleted asset cannot be
  // reported as a successful save. Validation failure leaves the draft editable.
  await transport.validate?.(safe)
  // A previous POST may have committed while its verification timed out.
  // Confirm that exact draft idempotently; do not write it again.
  if (current === desired) return safe
  const value = writeIconConfig(latest.value, safe)
  try {
    await transport.save({ ...latest, value })
    const verified = await transport.read()
    if (
      verified.value !== value ||
      canonicalIconConfig(readIconConfig(verified.value)) !== desired
    ) {
      throw new Error("保存结果未通过核对，请重新加载确认。")
    }
  } catch (cause) {
    const error = new Error(
      `保存结果待确认：${cause instanceof Error ? cause.message : String(cause)}`,
    )
    error.name = "IconSaveUncertain"
    throw error
  }
  return safe
}
