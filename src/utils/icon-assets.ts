import { createSignal } from "solid-js"
import { base_path } from "./config"
import {
  isUploadedIconId,
  type UploadedIconAsset,
  parseUploadedIconAsset,
} from "./icon-policy"

export type IconAsset = {
  id: string
  name: string
  url: string
  revision: string
  source: "builtin" | "uploaded"
}
export const [uploadedIconAssets, setUploadedIconAssets] = createSignal<
  IconAsset[]
>([])
export const uploadedIconURL = (id: string, revision?: string) => {
  if (!isUploadedIconId(id)) throw new Error("上传图标标识无效")
  return `${base_path}/icon/assets/${id}?v=${encodeURIComponent(revision || "current")}`
}
export const toUploadedIconAsset = (raw: UploadedIconAsset): IconAsset => {
  const item = parseUploadedIconAsset(raw)
  return {
    id: item.id,
    name: item.name,
    revision: item.revision,
    source: item.source,
    url: uploadedIconURL(item.id, item.revision),
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
export const updateUploadedIconAsset = (raw: UploadedIconAsset) => {
  const item = toUploadedIconAsset(raw)
  setUploadedIconAssets((old) => old.map(asset => asset.id === item.id ? item : asset))
}
export const removeLibraryIcon = (id: string) => {
  setUploadedIconAssets((old) => old.filter((asset) => asset.id !== id))
}
export const allIconAssets = () => [...uploadedIconAssets()]
export const iconAssetById = (id: string): IconAsset | undefined => {
  const known = uploadedIconAssets().find((asset) => asset.id === id)
  // Frontend rendering does not wait for a public catalog request.
  // A missing uploaded file falls back through AssetIcon's native error handler.
  return (
    known ??
    (isUploadedIconId(id)
      ? {
          id,
          name: "上传素材",
          revision: "",
          source: "uploaded",
          url: uploadedIconURL(id),
        }
      : undefined)
  )
}
