import {
  createEffect,
  createMemo,
  createSignal,
  For,
  onCleanup,
  onMount,
  Show,
} from "solid-js"
import { Portal } from "solid-js/web"
import {
  type UploadedIconAsset,
  type IconLibraryCatalog,
  iconUploadFileError,
} from "~/utils/icon-policy"
import {
  uploadedIconAssets,
  publishUploadedIconAssets,
  addUploadedIconAsset,
  setHiddenBuiltinIcons,
} from "~/utils/icon-assets"
import { uxText } from "~/utils/ux"
import {
  loadUploadedIcons,
  uploadIconFile,
  deleteLibraryIcon,
  type IconUploadRequest,
} from "./icon-upload"

export type IconLibraryAdapter = {
  load: (signal: AbortSignal) => Promise<IconLibraryCatalog>
  upload: (request: IconUploadRequest) => Promise<UploadedIconAsset>
  remove: (id: string, signal: AbortSignal) => Promise<void>
}
export const iconLibraryAdapter: IconLibraryAdapter = {
  load: loadUploadedIcons,
  upload: uploadIconFile,
  remove: deleteLibraryIcon,
}
type Job = {
  id: string
  file: File
  state: "queued" | "uploading" | "done" | "error"
  percent: number
  message: string
  asset?: UploadedIconAsset
}
export const IconUploader = (props: {
  adapter?: IconLibraryAdapter
  disabled: boolean
  busy: (value: boolean) => void
  unfinished?: (value: boolean) => void
  previewControl?: (open: boolean, close: () => void) => void
  libraryControl?: (from: HTMLButtonElement) => void
  catalogLoading?: (value: boolean) => void
}) => {
  const adapter = () => props.adapter ?? iconLibraryAdapter
  const [jobs, setJobs] = createSignal<Job[]>([])
  const [uploading, setUploading] = createSignal(false)
  const [catalogBusy, setCatalogBusy] = createSignal(false)
  const [catalogError, setCatalogError] = createSignal("")
  const [message, setMessage] = createSignal("")
  const [preview, setPreview] = createSignal<UploadedIconAsset>()
  const [previewFailed, setPreviewFailed] = createSignal(false)
  let picker!: HTMLInputElement
  let dialog!: HTMLDialogElement
  let previewImage!: HTMLImageElement
  let active = true
  let stopped = false
  let uploadController: AbortController | undefined
  let catalogController: AbortController | undefined
  let previewOpener: HTMLButtonElement | undefined
  const done = createMemo(
    () => jobs().filter((job) => job.state === "done").length,
  )
  const pending = () =>
    jobs().some((job) => job.state === "queued" || job.state === "error")
  createEffect(() => {
    props.unfinished?.(jobs().some((job) => job.state !== "done"))
  })
  const patchJob = (id: string, patch: Partial<Job>) =>
    setJobs((old) =>
      old.map((job) => (job.id === id ? { ...job, ...patch } : job)),
    )
  const loadCatalog = async () => {
    if (catalogBusy() || uploading()) return
    catalogController?.abort()
    const controller = new AbortController()
    catalogController = controller
    setCatalogBusy(true)
    props.catalogLoading?.(true)
    setCatalogError("")
    try {
      const catalog = await adapter().load(controller.signal)
      if (active && !controller.signal.aborted) {
        publishUploadedIconAssets(catalog.assets)
        setHiddenBuiltinIcons(catalog.hidden_builtins)
      }
    } catch (cause) {
      if (active && !controller.signal.aborted)
        setCatalogError(
          cause instanceof Error ? cause.message : "素材列表读取失败",
        )
    } finally {
      if (active && catalogController === controller) {
        setCatalogBusy(false)
        props.catalogLoading?.(false)
        catalogController = undefined
      }
    }
  }
  onMount(() => {
    void loadCatalog()
  })
  const run = async () => {
    if (uploading() || catalogBusy() || props.disabled || !pending()) return
    const work = jobs().filter(
      (job) => job.state === "queued" || job.state === "error",
    )
    stopped = false
    setUploading(true)
    props.busy(true)
    setMessage("")
    try {
      for (const job of work) {
        if (!active || stopped) break
        const controller = new AbortController()
        uploadController = controller
        patchJob(job.id, { state: "uploading", percent: 0, message: "" })
        try {
          const asset = await adapter().upload({
            file: job.file,
            requestId: job.id,
            signal: controller.signal,
            progress: (percent) => {
              if (active) patchJob(job.id, { percent })
            },
          })
          if (!active) return
          addUploadedIconAsset(asset)
          patchJob(job.id, { state: "done", percent: 100, asset, message: "" })
        } catch (cause) {
          if (!active) return
          patchJob(job.id, {
            state: "error",
            message: cause instanceof Error ? cause.message : "上传失败",
          })
          // Stop this batch on failure, retaining subsequent files and the same
          // idempotency key for each retry instead of blindly creating copies.
          break
        } finally {
          if (uploadController === controller) uploadController = undefined
        }
      }
      if (active)
        setMessage(
          pending()
            ? uxText(
                "还有未完成任务，请用原任务重试或刷新素材核对。任务仅保留在此页，离开前请先处理。",
                "Unfinished tasks remain. Retry the original tasks or refresh the library to verify. Tasks exist only on this page; resolve them before leaving.",
              )
            : uxText(
                "已上传素材已加入素材库；到类型卡片中选用，再保存更改。",
                "Uploaded assets were added to the library. Choose them on a type card, then save changes.",
              ),
        )
    } finally {
      if (active) {
        setUploading(false)
        props.busy(false)
      }
    }
  }
  const chooseFiles = (files: File[]) => {
    if (uploading() || catalogBusy() || props.disabled || !files.length) return
    if (files.length > 20) {
      setMessage(
        uxText(
          "每批最多选择 20 张，请分批上传。",
          "Choose up to 20 files per batch.",
        ),
      )
      return
    }
    if (
      pending() &&
      !window.confirm(
        uxText(
          "更换文件会放弃未完成的上传任务，已上传素材仍保留。继续吗？",
          "Replace unfinished upload tasks? Already uploaded assets remain saved.",
        ),
      )
    )
      return
    const invalid = files
      .map((file) => [file.name, iconUploadFileError(file)])
      .filter(([, error]) => error)
    if (invalid.length) {
      setMessage(invalid.map(([name, error]) => `${name}：${error}`).join("\n"))
      return
    }
    if (!window.crypto?.getRandomValues) {
      setMessage("当前浏览器不支持安全上传任务，请更新浏览器。")
      return
    }
    setJobs(
      files.map((file) => ({
        id: Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
          byte.toString(16).padStart(2, "0"),
        ).join(""),
        file,
        state: "queued",
        percent: 0,
        message: "",
      })),
    )
    setMessage("")
    void run()
  }
  const closePreview = () => {
    if (dialog.open) dialog.close()
    setPreview(undefined)
    props.previewControl?.(false, closePreview)
    if (previewOpener?.isConnected) previewOpener.focus({ preventScroll: true })
  }
  const clearJobs = () => {
    if (uploading() || catalogBusy() || props.disabled) return
    if (
      pending() &&
      !window.confirm(
        uxText(
          "清除未完成任务会丢失原重试标识；部分素材可能已保存，建议先刷新素材或用原任务重试核对。仍要清除吗？",
          "Clearing unfinished tasks loses their retry IDs. Some assets may already be saved; refresh or retry the original tasks first. Clear anyway?",
        ),
      )
    )
      return
    setJobs([])
    setMessage(
      uxText(
        "已清除任务记录，素材库未改动。",
        "Task records cleared; the library is unchanged.",
      ),
    )
  }
  const showPreview = (asset: UploadedIconAsset, from: HTMLButtonElement) => {
    previewOpener = from
    setPreview(asset)
    setPreviewFailed(!uploadedIconAssets().some((item) => item.id === asset.id))
    dialog.showModal()
    props.previewControl?.(true, closePreview)
  }
  const previewURL = () =>
    uploadedIconAssets().find((asset) => asset.id === preview()?.id)?.url
  onCleanup(() => {
    active = false
    stopped = true
    uploadController?.abort()
    catalogController?.abort()
    if (dialog?.open) dialog.close()
    previewImage?.removeAttribute("src")
  })
  return (
    <section
      class="im-upload"
      aria-label={uxText("上传图标素材", "Upload icon assets")}
      aria-busy={uploading()}
    >
      <header class="im-upload-heading">
        <div>
          <strong>{uxText("我的图标", "My icons")}</strong>
          <small>
            {uxText(
              `${uploadedIconAssets().length} 张已上传`,
              `${uploadedIconAssets().length} uploaded`,
            )}
          </small>
        </div>
        <div class="im-upload-actions">
          <button
            type="button"
            disabled={props.disabled || uploading() || catalogBusy()}
            onClick={(event) => props.libraryControl?.(event.currentTarget)}
            data-action="manage-library"
          >
            {uxText("管理素材", "Manage assets")}
          </button>
          <button
            type="button"
            disabled={props.disabled || uploading() || catalogBusy()}
            onClick={() => picker.click()}
            data-action="upload-icons"
          >
            {uxText("上传图标", "Upload icons")}
          </button>
          <button
            type="button"
            disabled={props.disabled || uploading() || catalogBusy()}
            onClick={() => void loadCatalog()}
            data-action="refresh-icons"
          >
            {catalogBusy()
              ? uxText("读取中…", "Loading…")
              : uxText("刷新素材", "Refresh assets")}
          </button>
        </div>
      </header>
      <input
        ref={picker}
        class="im-upload-file"
        type="file"
        accept=".png,image/png"
        multiple
        tabIndex={-1}
        hidden
        aria-label={uxText("选择图标文件", "Choose icon files")}
        onChange={(event) => {
          const files = Array.from(event.currentTarget.files ?? [])
          event.currentTarget.value = ""
          chooseFiles(files)
        }}
      />
      <p class="im-upload-rule">
        {uxText(
          "仅限 PNG，每张不超过 5 MB，不支持动画 PNG。可多选，自动保留透明背景、等比整理；上传不会自动替换站点图标。",
          "PNG only, up to 5 MB each; animated PNG is not supported. Multiple files accepted; transparency and proportions preserved. Uploading does not change site icons.",
        )}
      </p>
      <Show when={catalogError()}>
        <p class="im-error" role="alert">
          {catalogError()}
          {uxText(
            "。已有图标仍可用，请刷新素材重试。",
            ". Existing icons remain usable; refresh assets to retry.",
          )}
        </p>
      </Show>
      <Show when={jobs().length}>
        <details class="im-upload-jobs" open>
          <summary>
            {uxText(
              `上传进度 ${done()} / ${jobs().length}`,
              `Upload progress ${done()} / ${jobs().length}`,
            )}
          </summary>
          <ol>
            <For each={jobs()}>
              {(job) => (
                <li data-upload-state={job.state}>
                  <span>{job.file.name}</span>
                  <small>
                    {job.state === "done"
                      ? uxText("已上传", "Uploaded")
                      : job.state === "uploading"
                        ? `${job.percent}%`
                        : job.state === "queued"
                          ? uxText("待上传", "Queued")
                          : job.message}
                  </small>
                  <Show when={job.asset}>
                    <button
                      type="button"
                      onClick={(event) =>
                        showPreview(job.asset!, event.currentTarget)
                      }
                      aria-label={uxText(
                        `预览 ${job.file.name}`,
                        `Preview ${job.file.name}`,
                      )}
                    >
                      {uxText("预览", "Preview")}
                    </button>
                  </Show>
                </li>
              )}
            </For>
          </ol>
          <Show
            when={uploading()}
            fallback={
              <Show when={pending()}>
                <button
                  type="button"
                  disabled={props.disabled || catalogBusy()}
                  onClick={() => void run()}
                  data-action="retry-upload"
                >
                  {uxText("重试未完成上传", "Retry unfinished uploads")}
                </button>
              </Show>
            }
          >
            <button
              type="button"
              onClick={() => {
                stopped = true
                uploadController?.abort()
              }}
              data-action="pause-upload"
            >
              {uxText("暂停上传", "Pause uploads")}
            </button>
          </Show>
          <Show when={!uploading()}>
            <button
              type="button"
              disabled={props.disabled || catalogBusy()}
              onClick={clearJobs}
              data-action="clear-upload"
            >
              {uxText("清除任务", "Clear tasks")}
            </button>
          </Show>
        </details>
      </Show>
      <Show when={message()}>
        <p class="im-upload-message" role="status">
          {message()}
        </p>
      </Show>
      <Portal>
        <dialog
          ref={dialog}
          class="icon-manager im-upload-preview"
          aria-label={uxText("上传图标预览", "Uploaded icon preview")}
          onCancel={(event) => {
            event.preventDefault()
            closePreview()
          }}
          onKeyDown={(event) => {
            if (event.key === "Tab") {
              event.preventDefault()
              dialog.querySelector<HTMLButtonElement>("button")?.focus()
            }
          }}
        >
          <h2>{preview()?.name}</h2>
          <img
            ref={previewImage}
            src={previewURL()}
            alt={preview()?.name}
            width="256"
            height="256"
            hidden={previewFailed()}
            onError={() => setPreviewFailed(true)}
          />
          <Show when={previewFailed()}>
            <p role="alert">
              {uxText(
                "素材已移除或预览加载失败，请关闭后刷新素材库核对。",
                "The asset was removed or its preview failed; close and refresh the library to verify.",
              )}
            </p>
          </Show>
          <p>
            {uxText(
              "已加入素材库，点击类型卡片即可选用。",
              "Added to your library; open a type card to use it.",
            )}
          </p>
          <button type="button" class="im-primary" onClick={closePreview}>
            {uxText("关闭预览", "Close preview")}
          </button>
        </dialog>
      </Portal>
    </section>
  )
}
