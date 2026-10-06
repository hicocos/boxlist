import { createSignal } from "solid-js"
import { base_path } from "./config"
import {
  isUploadedIconId,
  type UploadedIconAsset,
  parseUploadedIconAsset,
} from "./icon-policy"
import cab from "~/assets/file-icons/pack-cab.webp"
import doc from "~/assets/file-icons/pack-doc.webp"
import docx from "~/assets/file-icons/pack-docx.webp"
import mp3 from "~/assets/file-icons/pack-mp3.webp"
import pdf from "~/assets/file-icons/pack-pdf.webp"
import ppt from "~/assets/file-icons/pack-ppt.webp"
import pptx from "~/assets/file-icons/pack-pptx.webp"
import psd from "~/assets/file-icons/pack-psd.webp"
import rar from "~/assets/file-icons/pack-rar.webp"
import txt from "~/assets/file-icons/pack-txt.webp"
import wav from "~/assets/file-icons/pack-wav.webp"
import wma from "~/assets/file-icons/pack-wma.webp"
import xlsx from "~/assets/file-icons/pack-xlsx.webp"
import zip from "~/assets/file-icons/pack-zip.webp"
import download from "~/assets/file-icons/pack-download.webp"
import image from "~/assets/file-icons/pack-image.webp"
import folder from "~/assets/file-icons/pack-folder.webp"
import documentIcon from "~/assets/file-icons/pack-document.webp"
import video from "~/assets/file-icons/pack-video.webp"
import music from "~/assets/file-icons/pack-music.webp"
import smile from "~/assets/file-icons/legacy-smile.webp"

export type IconAsset = {
  id: string
  name: string
  url: string
  family: "anime" | "simple" | "legacy" | "uploaded"
  group: string
  targets: string[]
}
export const ICON_ASSETS: IconAsset[] = [
  {
    id: "pack-folder",
    name: "文件夹.png",
    url: folder,
    family: "anime",
    group: "folder",
    targets: ["folder"],
  },
  {
    id: "legacy-smile",
    name: "原笑脸文件夹",
    url: smile,
    family: "legacy",
    group: "folder",
    targets: ["folder"],
  },
  {
    id: "pack-music",
    name: "音乐.png",
    url: music,
    family: "simple",
    group: "audio",
    targets: ["audio"],
  },
  {
    id: "pack-mp3",
    name: "mp3.png",
    url: mp3,
    family: "anime",
    group: "audio",
    targets: ["ext:mp3"],
  },
  {
    id: "pack-wav",
    name: "wav.png",
    url: wav,
    family: "anime",
    group: "audio",
    targets: ["ext:wav"],
  },
  {
    id: "pack-wma",
    name: "wma.png",
    url: wma,
    family: "anime",
    group: "audio",
    targets: ["ext:wma"],
  },
  {
    id: "pack-video",
    name: "视频.png",
    url: video,
    family: "simple",
    group: "video",
    targets: ["video"],
  },
  {
    id: "pack-image",
    name: "图片.png",
    url: image,
    family: "simple",
    group: "image",
    targets: ["image"],
  },
  {
    id: "pack-document",
    name: "文档.png",
    url: documentIcon,
    family: "simple",
    group: "text",
    targets: ["text", "other"],
  },
  {
    id: "pack-txt",
    name: "txt.png",
    url: txt,
    family: "anime",
    group: "text",
    targets: ["ext:txt"],
  },
  {
    id: "pack-doc",
    name: "doc.png",
    url: doc,
    family: "anime",
    group: "office",
    targets: ["ext:doc"],
  },
  {
    id: "pack-docx",
    name: "docx.png",
    url: docx,
    family: "anime",
    group: "office",
    targets: ["ext:docx"],
  },
  {
    id: "pack-xlsx",
    name: "xlsx.png",
    url: xlsx,
    family: "anime",
    group: "office",
    targets: ["ext:xlsx"],
  },
  {
    id: "pack-ppt",
    name: "ppt.png",
    url: ppt,
    family: "anime",
    group: "office",
    targets: ["ext:ppt"],
  },
  {
    id: "pack-pptx",
    name: "pptx.png",
    url: pptx,
    family: "anime",
    group: "office",
    targets: ["ext:pptx"],
  },
  {
    id: "pack-pdf",
    name: "pdf.png",
    url: pdf,
    family: "anime",
    group: "office",
    targets: ["ext:pdf"],
  },
  {
    id: "pack-psd",
    name: "psd.png",
    url: psd,
    family: "anime",
    group: "office",
    targets: ["ext:psd"],
  },
  {
    id: "pack-cab",
    name: "cab.png",
    url: cab,
    family: "anime",
    group: "archive",
    targets: ["ext:cab"],
  },
  {
    id: "pack-rar",
    name: "rar.png",
    url: rar,
    family: "anime",
    group: "archive",
    targets: ["ext:rar"],
  },
  {
    id: "pack-zip",
    name: "zip.png",
    url: zip,
    family: "anime",
    group: "archive",
    targets: ["ext:zip"],
  },
  {
    id: "pack-download",
    name: "下载.png",
    url: download,
    family: "simple",
    group: "download",
    targets: ["download"],
  },
]
export const [uploadedIconAssets, setUploadedIconAssets] = createSignal<
  IconAsset[]
>([])
export const uploadedIconURL = (id: string) => {
  if (!isUploadedIconId(id)) throw new Error("上传图标标识无效")
  return `${base_path}/icon/assets/${id}.webp`
}
export const toUploadedIconAsset = (raw: UploadedIconAsset): IconAsset => {
  const item = parseUploadedIconAsset(raw)
  return {
    id: item.id,
    name: item.name,
    url: uploadedIconURL(item.id),
    family: "uploaded",
    group: "uploaded",
    targets: [],
  }
}
export const publishUploadedIconAssets = (items: UploadedIconAsset[]) =>
  setUploadedIconAssets(items.map(toUploadedIconAsset))
export const addUploadedIconAsset = (raw: UploadedIconAsset) => {
  const item = toUploadedIconAsset(raw)
  setUploadedIconAssets((old) => [
    item,
    ...old.filter((asset) => asset.id !== item.id),
  ])
}
export const [hiddenBuiltinIcons, setHiddenBuiltinIcons] = createSignal<
  string[]
>([])
export const removeLibraryIcon = (id: string) => {
  if (isUploadedIconId(id))
    setUploadedIconAssets((old) => old.filter((asset) => asset.id !== id))
  else if (ICON_ASSETS.some((asset) => asset.id === id))
    setHiddenBuiltinIcons((old) => [...new Set([...old, id])])
}
export const allIconAssets = () => [
  ...ICON_ASSETS.filter((asset) => !hiddenBuiltinIcons().includes(asset.id)),
  ...uploadedIconAssets(),
]
export const iconAssetById = (id: string): IconAsset | undefined => {
  const known = [...ICON_ASSETS, ...uploadedIconAssets()].find(
    (asset) => asset.id === id,
  )
  // Frontend rendering does not wait for a public catalog request.
  // A missing uploaded file falls back through AssetIcon's native error handler.
  return (
    known ??
    (isUploadedIconId(id)
      ? {
          id,
          name: "上传素材",
          url: uploadedIconURL(id),
          family: "uploaded",
          group: "uploaded",
          targets: [],
        }
      : undefined)
  )
}
export const suggestedIconAssets = (target: { id: string; group: string }) => {
  const group = target.group === "general" ? target.id : target.group
  return allIconAssets().filter(
    (asset) =>
      asset.family === "uploaded" ||
      asset.targets.includes(target.id) ||
      asset.group === group ||
      (target.id === "other" && asset.id === "pack-document"),
  )
}
