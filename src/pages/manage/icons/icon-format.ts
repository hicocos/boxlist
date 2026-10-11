import { uxText } from "~/utils/ux"

// The server verifies original filename and content agree before storing an asset.
export const iconFormatLabel = (name: string, source?: "builtin" | "uploaded") => {
  const format = /\.gif$/i.test(name)
    ? uxText("动态图标（GIF）", "Animated icon (GIF)", "動態圖示（GIF）")
    : uxText("静态图标（PNG）", "Static icon (PNG)", "靜態圖示（PNG）")
  return source === "builtin" ? `${uxText("内置", "Built-in", "內建")} · ${format}` : format
}
