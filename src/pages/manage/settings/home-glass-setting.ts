import { Flag, Group, Resp, SettingItem, Type } from "~/types"
import { r } from "~/utils"
import {
  publishHomeGlassSettings,
  backgroundCss,
  DEFAULT_HOME_BACKGROUND,
  DEFAULT_HOME_TRANSPARENCY,
  glassVariables,
  HOME_BACKGROUND_KEY,
  HOME_GLASS_STYLE_ID,
  HOME_TRANSPARENCY_KEY,
  parseHomeTransparency,
  readHomeGlassSettings,
  validateHomeBackground,
} from "~/app/glass-theme"

const block =
  /<!-- OPENLIST-HOME-GLASS-START -->[\s\S]*?<!-- OPENLIST-HOME-GLASS-END -->\s*/g
export const isHomeGlassSetting = (key: string) =>
  key === HOME_TRANSPARENCY_KEY || key === HOME_BACKGROUND_KEY

export const homeGlassSettings = (head?: string): SettingItem[] => {
  const values =
    head === undefined
      ? {
          transparency: DEFAULT_HOME_TRANSPARENCY,
          background: DEFAULT_HOME_BACKGROUND,
        }
      : readHomeGlassSettings(head)
  return [
    {
      key: HOME_TRANSPARENCY_KEY,
      value: String(values.transparency),
      type: Type.Number,
      options: "",
      group: Group.STYLE,
      flag: Flag.PUBLIC,
      help: "percent",
    },
    {
      key: HOME_BACKGROUND_KEY,
      value: values.background,
      type: Type.String,
      options: "",
      group: Group.STYLE,
      flag: Flag.PUBLIC,
      help: "",
    },
  ]
}
const getHead = async () => {
  const resp: Resp<SettingItem> = await r.get(
    "/admin/setting/get?key=customize_head",
  )
  if (resp.code !== 200) throw new Error(resp.message)
  return resp.data
}
export const loadHomeGlassSettings = async () =>
  homeGlassSettings((await getHead()).value)
export const validateHomeGlassSettings = (items: SettingItem[]) => {
  const transparency = parseHomeTransparency(
    items.find((i) => i.key === HOME_TRANSPARENCY_KEY)?.value ??
      String(DEFAULT_HOME_TRANSPARENCY),
  )
  const background = validateHomeBackground(
    items.find((i) => i.key === HOME_BACKGROUND_KEY)?.value ??
      DEFAULT_HOME_BACKGROUND,
  )
  return { transparency, background }
}
export const saveHomeGlassSettings = async (items: SettingItem[]) => {
  const { transparency, background } = validateHomeGlassSettings(items)
  const head = await getHead()
  // Styles, not an executable script: configuration also works under strict CSP.
  // Percent-encoded metadata prevents URL strings from injecting HTML/CSS.
  const css = Object.entries(glassVariables(transparency))
    .map(([k, v]) => `${k}: ${v};`)
    .join(" ")
  const html = `<!-- OPENLIST-HOME-GLASS-START -->\n<style id="${HOME_GLASS_STYLE_ID}" data-background-image="${encodeURIComponent(background)}">:root { ${css} --home-background-image: ${backgroundCss(background)}; }</style>\n<!-- OPENLIST-HOME-GLASS-END -->`
  const resp: Resp<unknown> = await r.post("/admin/setting/save", [
    { ...head, value: head.value.replace(block, "").trimEnd() + "\n" + html },
  ])
  if (resp.code !== 200) throw new Error(resp.message)
  const verifiedHead = (await getHead()).value
  const verified = readHomeGlassSettings(verifiedHead)
  if (
    verified.transparency !== transparency ||
    verified.background !== background ||
    verifiedHead.match(block)?.length !== 1
  ) {
    throw new Error("Home appearance settings were not saved")
  }
  publishHomeGlassSettings(transparency, background)
}
