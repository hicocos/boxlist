export type LocalSettingGroup = "appearance" | "images" | "editor" | "downloads"

export interface LocalSetting {
  key: string
  default: string
  group: LocalSettingGroup
  type?: "select" | "boolean" | "number" | "password" | "url"
  options?: string[]
  hidden?: boolean
  sensitive?: boolean
  min?: number
  max?: number
}

/** Explicit membership keeps resets away from credentials and reading history. */
export const initialLocalSettings: LocalSetting[] = [
  {
    key: "global_default_layout",
    default: "list",
    type: "select",
    options: ["list", "grid", "image"],
    group: "appearance",
  },
  {
    key: "show_sidebar",
    default: "none",
    type: "select",
    options: ["none", "visible"],
    group: "appearance",
  },
  {
    key: "show_count_msg",
    default: "none",
    type: "select",
    options: ["none", "visible"],
    group: "appearance",
  },
  {
    key: "position_of_header_navbar",
    default: "static",
    type: "select",
    options: ["static", "sticky", "only_navbar_sticky"],
    group: "appearance",
  },
  {
    key: "grid_item_size",
    default: "90",
    type: "number",
    min: 60,
    max: 240,
    group: "appearance",
  },
  {
    key: "list_item_filename_overflow",
    default: "ellipsis",
    type: "select",
    options: ["ellipsis", "scrollable", "multi_line"],
    group: "appearance",
  },
  {
    key: "open_item_on_checkbox",
    default: "direct",
    type: "select",
    options: ["direct", "dblclick", "disable_while_checked"],
    group: "appearance",
  },
  {
    key: "show_folder_in_image_view",
    default: "top",
    type: "select",
    options: ["top", "bottom", "none"],
    group: "images",
  },
  {
    key: "show_gallery_thumbnails",
    default: "visible",
    type: "select",
    options: ["none", "visible"],
    group: "images",
  },
  {
    key: "editor_font_size",
    default: "14",
    type: "number",
    min: 8,
    max: 40,
    group: "editor",
  },
  {
    key: "editor_word_wrap",
    default: "false",
    type: "select",
    options: ["false", "true"],
    group: "editor",
  },
  {
    key: "editor_minimap",
    default: "true",
    type: "select",
    options: ["false", "true"],
    group: "editor",
  },
  {
    key: "aria2_rpc_url",
    default: "http://localhost:6800/jsonrpc",
    type: "url",
    group: "downloads",
  },
  {
    key: "aria2_rpc_secret",
    default: "",
    type: "password",
    sensitive: true,
    group: "downloads",
  },
]

/** Empty/invalid drafts revert; finite numbers round and clamp to safe pixels. */
export function normalizeLocalNumber(
  setting: LocalSetting,
  draft: string | null | undefined,
  previous: string | null | undefined = setting.default,
): string {
  const parse = (value: string | null | undefined) =>
    typeof value === "string" && /^[+-]?\d+(?:\.\d+)?$/.test(value.trim())
      ? Number(value)
      : NaN
  const candidate = parse(draft)
  const saved = parse(previous)
  const number = Number.isFinite(candidate)
    ? candidate
    : Number.isFinite(saved)
      ? saved
      : Number(setting.default)
  return String(
    Math.min(
      setting.max ?? Infinity,
      Math.max(setting.min ?? 1, Math.round(number)),
    ),
  )
}

export function localSettingResetEntries(group: LocalSettingGroup) {
  return initialLocalSettings
    .filter((setting) => setting.group === group && !setting.sensitive)
    .map((setting) => [setting.key, setting.default] as const)
}
