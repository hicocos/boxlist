import { Resp, SettingItem } from "~/types"
import { r } from "~/utils/request"
import {
  type IconConfig,
  readIconConfig,
  persistIconConfig,
  DEFAULT_ICON,
  assertIconAssetsAvailable,
} from "~/utils/icon-policy"
import { publishSiteIconConfig } from "~/components/SiteIcon"
import { withHeadWriteLock } from "~/utils/head-write-lock"
import { loadUploadedIcons } from "./icon-upload"

const readHead = async (): Promise<SettingItem> => {
  const response = (await r.get("/admin/setting/get?key=customize_head", {
    timeout: 15000,
  })) as unknown as Resp<SettingItem>
  if (
    response.code !== 200 ||
    !response.data ||
    typeof response.data.value !== "string"
  ) {
    throw new Error(response.message || "无法读取图标配置，请重试。")
  }
  return response.data
}
export const loadIconSettings = async () => {
  const saved = readIconConfig((await readHead()).value)
  publishSiteIconConfig(saved)
  return saved
}
export const saveIconSettings = async (
  draft: IconConfig,
  baseline: IconConfig,
) => {
  const saved = await withHeadWriteLock(() =>
    persistIconConfig(
      {
        read: readHead,
        validate: async (config) => {
          // Restoring native defaults stays possible if the asset service is down.
          if (
            !Object.values(config.selections).some((id) => id !== DEFAULT_ICON)
          )
            return
          const catalog = await loadUploadedIcons(new AbortController().signal)
          assertIconAssetsAvailable(config, catalog)
        },
        save: async (item) => {
          const response = (await r.post("/admin/setting/save", [item], {
            timeout: 15000,
          })) as unknown as Resp<unknown>
          if (response.code !== 200)
            throw new Error(response.message || "保存失败，请重试。")
        },
      },
      draft,
      baseline,
    ),
  )
  publishSiteIconConfig(saved)
  return saved
}
