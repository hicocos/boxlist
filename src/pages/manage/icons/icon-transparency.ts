import { r } from "~/utils/request"
import { base_path } from "~/utils/config"
import { isUploadedIconId, parseUploadedIconAsset } from "~/utils/icon-policy"
import type { UploadedIconAsset } from "~/utils/icon-policy"

export interface TransparencyJob {
  asset_id: string
  status: "processing" | "done" | "unchanged" | "failed"
  progress: number
  message?: string
  asset?: UploadedIconAsset
}

export class TransparencyRequestError extends Error {
  constructor(message: string, public status: number) { super(message) }
}
export async function requestTransparency(id: string, signal: AbortSignal, revision?: string): Promise<TransparencyJob> {
  if (!isUploadedIconId(id)) throw new Error("素材标识无效")
  const authorization = r.defaults.headers.common.Authorization
  if (typeof authorization !== "string" || !authorization) throw new Error("请先登录管理员账户。")
  const response = await fetch(`${base_path}/icon/api/assets/${id}/transparent`, {
    method: revision === undefined ? "GET" : "POST",
    signal: AbortSignal.any([signal, AbortSignal.timeout(15000)]),
    headers: { Authorization: authorization, ...(revision === undefined ? {} : { "Content-Type": "application/json" }) },
    body: revision === undefined ? undefined : JSON.stringify({ revision }),
  })
  const result = await response.json()
  if (!response.ok || result.code !== 200) throw new TransparencyRequestError(result.message || "处理状态暂不可用，请刷新素材后核对。", response.status)
  const job = result.data as TransparencyJob
  if (!job || job.asset_id !== id || !["processing", "done", "unchanged", "failed"].includes(job.status) ||
    !Number.isFinite(job.progress) || job.progress < 0 || job.progress > 100 ||
    (job.message !== undefined && typeof job.message !== "string")) throw new Error("处理状态无效")
  return { ...job, asset: job.asset ? parseUploadedIconAsset(job.asset) : undefined }
}
