import {
  BsFileEarmarkWordFill,
  BsFileEarmarkExcelFill,
  BsFileEarmarkPptFill,
  BsFileEarmarkPdfFill,
  BsFileEarmarkPlayFill,
  BsFileEarmarkMusicFill,
  BsFileEarmarkFontFill,
  BsFileEarmarkImageFill,
  BsFileEarmarkMinusFill,
  BsApple,
  BsWindows,
  BsFileEarmarkZipFill,
  BsMarkdownFill,
} from "solid-icons/bs"
import {
  FaSolidDatabase,
  FaSolidBook,
  FaSolidCompactDisc,
  FaSolidLink,
} from "solid-icons/fa"
import { IoFolder } from "solid-icons/io"
import { AiOutlineCloudDownload } from "solid-icons/ai"
import { AssetIcon, siteIconConfig } from "~/components/SiteIcon"
import { ImAndroid } from "solid-icons/im"
import { Obj, ObjType } from "~/types"
import { ext } from "./path"
import {
  VscodeIconsFileTypeAi2,
  VscodeIconsFileTypePhotoshop2,
} from "~/components/icons"
import { SiAsciinema } from "solid-icons/si"
import { isArchive } from "~/store/archive"
import { type IconTarget, resolveIconAsset } from "./icon-policy"
import type { IconProps, IconTypes } from "solid-icons"
import { createComponent, mergeProps } from "solid-js"

const iconMap: Record<string, IconTypes> = {
  "dmg,ipa,plist,tipa": BsApple,
  "exe,msi": BsWindows,
  apk: ImAndroid,
  db: FaSolidDatabase,
  md: BsMarkdownFill,
  epub: FaSolidBook,
  iso: FaSolidCompactDisc,
  m3u8: BsFileEarmarkPlayFill,
  "doc,docx": BsFileEarmarkWordFill,
  "xls,xlsx": BsFileEarmarkExcelFill,
  "ppt,pptx": BsFileEarmarkPptFill,
  pdf: BsFileEarmarkPdfFill,
  psd: VscodeIconsFileTypePhotoshop2,
  ai: VscodeIconsFileTypeAi2,
  url: FaSolidLink,
  cast: SiAsciinema,
}
// Export native glyphs for accurate manager previews and explicit defaults.
export const getDefaultIconByTypeAndName = (
  type: number,
  name: string,
  archive = isArchive(name),
): IconTypes => {
  if (type !== ObjType.FOLDER) {
    for (const [extensions, icon] of Object.entries(iconMap)) {
      if (extensions.split(",").includes(ext(name).toLowerCase())) return icon
    }
    if (archive) return BsFileEarmarkZipFill
  }
  switch (type) {
    case ObjType.FOLDER:
      return IoFolder
    case ObjType.VIDEO:
      return BsFileEarmarkPlayFill
    case ObjType.AUDIO:
      return BsFileEarmarkMusicFill
    case ObjType.TEXT:
      return BsFileEarmarkFontFill
    case ObjType.IMAGE:
      return BsFileEarmarkImageFill
    default:
      return BsFileEarmarkMinusFill
  }
}
export const getDefaultIconForTarget = (target: IconTarget): IconTypes =>
  target.id === "download"
    ? AiOutlineCloudDownload
    : getDefaultIconByTypeAndName(
        target.type,
        target.example,
        target.id === "archive" || isArchive(target.example),
      )

export const getIconByTypeAndName = (type: number, name: string): IconTypes => {
  const native = getDefaultIconByTypeAndName(type, name)
  return (props: IconProps) =>
    createComponent(
      AssetIcon,
      mergeProps(props, {
        fallback: native,
        get asset() {
          return resolveIconAsset(siteIconConfig(), type, name, isArchive(name))
        },
      }),
    )
}
export const getIconByObj = (obj: Pick<Obj, "type" | "name">) =>
  getIconByTypeAndName(obj.type, obj.name)
