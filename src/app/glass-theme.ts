// Only panel backgrounds change transparency; text/icons/images stay opaque.
import { setConfiguredBackground } from "./background-priority"
export const DEFAULT_HOME_TRANSPARENCY = 60
export const DEFAULT_HOME_BACKGROUND = ""
export const HOME_TRANSPARENCY_KEY = "home_transparency"
export const HOME_BACKGROUND_KEY = "home_background_image"
export const HOME_GLASS_STYLE_ID = "openlist-home-glass-config"
export const HOME_GLASS_SYNC_KEY = "openlist-home-glass-sync"

export const parseHomeTransparency = (value: string): number => {
  if (!/^\d+(?:\.\d+)?$/.test(value.trim())) {
    throw new Error("Home transparency must be a number from 0 to 100")
  }
  const number = Number(value)
  if (!Number.isFinite(number) || number < 0 || number > 100) {
    throw new Error("Home transparency must be a number from 0 to 100")
  }
  return number
}

export const validateHomeBackground = (value: string): string => {
  const url = value.trim()
  if (!url) return ""
  if (url.startsWith("/") && !url.startsWith("//") && !/[\s<>]/.test(url))
    return url
  const parsed = new URL(url)
  if (
    !["https:", "http:"].includes(parsed.protocol) ||
    parsed.username ||
    parsed.password ||
    /[\s<>]/.test(url)
  ) {
    throw new Error("Use an HTTP(S) image URL or a site-relative path")
  }
  return url
}

export const readHomeGlassSettings = (head: string) => {
  const style = head.match(
    /<style\b[^>]*\bid=["']openlist-home-glass-config["'][^>]*>([\s\S]*?)<\/style>/,
  )
  const value = style?.[1].match(/--home-transparency:\s*([\d.]+)\s*;/)?.[1]
  let transparency = DEFAULT_HOME_TRANSPARENCY
  let background = DEFAULT_HOME_BACKGROUND
  try {
    if (value) transparency = parseHomeTransparency(value)
  } catch {
    /* Invalid legacy data uses the default. */
  }
  try {
    const encoded = style?.[0].match(/data-background-image="([^"]*)"/)?.[1]
    if (encoded !== undefined)
      background = validateHomeBackground(decodeURIComponent(encoded))
  } catch {
    /* Invalid legacy data uses the default. */
  }
  return { transparency, background }
}

export const glassVariables = (percent: number) => {
  const alpha = Number(((100 - percent) / 100).toFixed(4))
  return {
    "--home-transparency": String(percent),
    "--home-glass-alpha": String(alpha),
    "--home-glass-hover-alpha": String(
      Math.min(1, Number((alpha * 1.75).toFixed(4))),
    ),
  }
}

export const backgroundCss = (url: string) =>
  url ? `url(${JSON.stringify(url).replace(/</g, "\\3c ")})` : "none"

export const applyHomeGlassSettings = (percent: number, background: string) => {
  for (const [key, value] of Object.entries(glassVariables(percent))) {
    document.documentElement.style.setProperty(key, value)
  }
  document.documentElement.style.setProperty(
    "--home-background-image",
    backgroundCss(background),
  )
  setConfiguredBackground(background)
}

export const publishHomeGlassSettings = (
  percent: number,
  background: string,
) => {
  applyHomeGlassSettings(percent, background)
  try {
    // A notification, not the source of truth: fresh loads use server HTML.
    // Storage events synchronize other tabs without polling or reloading media.
    localStorage.setItem(
      HOME_GLASS_SYNC_KEY,
      JSON.stringify({ percent, background, revision: Date.now() }),
    )
  } catch {
    /* Storage disabled: save still succeeds, refresh other tabs. */
  }
}

export const listenForHomeGlassSettings = () => {
  const handler = (event: StorageEvent) => {
    if (event.key !== HOME_GLASS_SYNC_KEY || !event.newValue) return
    try {
      const data = JSON.parse(event.newValue)
      applyHomeGlassSettings(
        parseHomeTransparency(String(data.percent)),
        validateHomeBackground(data.background),
      )
    } catch {
      /* Ignore unrelated/invalid storage data. */
    }
  }
  window.addEventListener("storage", handler)
  return () => window.removeEventListener("storage", handler)
}

export const bootstrapGlassTheme = () => {
  const config = document.getElementById(HOME_GLASS_STYLE_ID)
  const { transparency, background } = readHomeGlassSettings(
    config?.outerHTML ?? "",
  )
  applyHomeGlassSettings(transparency, background)
}
