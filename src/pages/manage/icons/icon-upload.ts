import axios from "axios"
import { base_path } from "~/utils/config"
import { withHeadWriteLock } from "~/utils/head-write-lock"
import { r } from "~/utils/request"
import {
  type UploadedIconAsset,
  type IconLibraryCatalog,
  isKnownIconAssetId,
  parseUploadedIconAsset,
  parseIconLibraryCatalog,
} from "~/utils/icon-policy"

// Same origin only; use the current native API authorization header without
// writing/printing/storing credentials or adding another login mechanism.
const client = axios.create({ timeout: 45000, withCredentials: false })
const prefix = () => `${base_path}/icon-library/api`
const responseData = (response: {
  code?: number
  message?: string
  data?: unknown
}) => {
  if (response?.code !== 200)
    throw new Error(response?.message || "图标素材服务不可用，请重试。")
  return response.data
}
export const loadUploadedIcons = async (
  signal: AbortSignal,
): Promise<IconLibraryCatalog> => {
  try {
    const response = await client.get(`${prefix()}/catalog`, {
      timeout: 15000,
      signal,
    })
    return parseIconLibraryCatalog(responseData(response.data))
  } catch (cause) {
    throw transportError(cause)
  }
}
const transportError = (cause: unknown): Error => {
  if (axios.isCancel(cause))
    return new Error("已取消；未确认的上传可用原任务重试。")
  if (axios.isAxiosError(cause)) {
    const message = cause.response?.data?.message
    return new Error(
      typeof message === "string"
        ? message
        : "请求结果待确认，请重试或刷新素材后核对。",
    )
  }
  return cause instanceof Error ? cause : new Error("图标素材请求失败")
}
export const deleteLibraryIcon = async (
  id: string,
  signal: AbortSignal,
): Promise<void> => {
  if (!isKnownIconAssetId(id)) throw new Error("图标标识无效")
  const currentAuthorization = r.defaults.headers.common.Authorization
  if (typeof currentAuthorization !== "string" || !currentAuthorization)
    throw new Error("请先登录管理员账户后删除图标。")
  try {
    await withHeadWriteLock(async () => {
      signal.throwIfAborted()
      const authorization = r.defaults.headers.common.Authorization
      if (authorization !== currentAuthorization)
        throw new Error("账户状态已变化，请重新核对后删除。")
      const response = await client.delete(`${prefix()}/assets/${id}`, {
        signal,
        timeout: 15000,
        headers: { Authorization: authorization },
      })
      const data = responseData(response.data) as {
        id?: string
        removed?: boolean
      }
      if (data?.id !== id || data.removed !== true)
        throw new Error("删除结果待确认，请刷新素材后核对。")
      const catalog = await loadUploadedIcons(signal)
      if (
        id.startsWith("upload-")
          ? catalog.assets.some((item) => item.id === id)
          : !catalog.hidden_builtins.includes(id)
      )
        throw new Error("删除结果待确认，请刷新素材后核对。")
    })
  } catch (cause) {
    throw transportError(cause)
  }
}
export type IconUploadRequest = {
  file: File
  requestId: string
  signal: AbortSignal
  progress: (percent: number) => void
}
export const uploadIconFile = async (
  request: IconUploadRequest,
): Promise<UploadedIconAsset> => {
  const authorization = r.defaults.headers.common.Authorization
  if (typeof authorization !== "string" || !authorization)
    throw new Error("请先登录管理员账户后上传图标。")
  if (!/^[a-f0-9]{32}$/.test(request.requestId))
    throw new Error("上传任务标识无效")
  try {
    const response = await client.post(`${prefix()}/upload`, request.file, {
      signal: request.signal,
      headers: {
        "Content-Type": "application/octet-stream",
        "X-Icon-Name": encodeURIComponent(request.file.name),
        "X-Upload-Id": request.requestId,
        Authorization: authorization,
      },
      onUploadProgress: (event) =>
        request.progress(
          Math.min(
            99,
            Math.round(
              (event.loaded / (event.total || request.file.size)) * 100,
            ),
          ),
        ),
    })
    const data = responseData(response.data) as { asset?: unknown }
    const uploaded = parseUploadedIconAsset(data?.asset)
    // A successful POST is not enough: verify this exact immutable asset in
    // the durable catalog. On a timeout the same request key is retried.
    const catalog = await loadUploadedIcons(request.signal)
    const confirmed = catalog.assets.find((item) => item.id === uploaded.id)
    if (!confirmed || JSON.stringify(confirmed) !== JSON.stringify(uploaded))
      throw new Error("上传结果待确认，请用原任务重试核对。")
    return uploaded
  } catch (cause) {
    throw transportError(cause)
  }
}
