import { createSignal, Show } from "solid-js"
import type { IconAsset } from "~/utils/icon-assets"
import { updateUploadedIconAsset } from "~/utils/icon-assets"
import { uxText } from "~/utils/ux"
import { requestTransparency, TransparencyRequestError } from "./icon-transparency"

// Shared by the card and library dialog; changing views never starts a second job.
const [states, setStates] = createSignal<Record<string, { busy: boolean; progress: number; text: string; uncertain?: boolean }>>({})
const workers = new Map<string, AbortController>()

export const iconTransparencyBusy = (id: string) => !!states()[id]?.busy

export function TransparentIconAction(props: { asset: IconAsset; disabled?: boolean }) {
  const state = () => states()[props.asset.id]
  const process = async () => {
    const id = props.asset.id
    if (workers.has(id) || props.disabled) return
    const checking = state()?.uncertain
    if (!checking && !window.confirm(uxText(
      `为“${props.asset.name}”去除边缘连通的白色背景？成功后直接覆盖该素材，所有已应用的图标会更新，无法恢复。内部白色细节会尽量保留。`,
      `Remove the edge-connected white background from "${props.asset.name}"? This permanently replaces the asset and updates every use. There is no restore. Interior white details will be preserved where possible.`,
    ))) return
    const controller = new AbortController()
    workers.set(id, controller)
    const set = (value: { busy: boolean; progress: number; text: string; uncertain?: boolean }) => setStates(old => ({ ...old, [id]: value }))
    set({ busy: true, progress: 0, text: uxText("正在提交…", "Starting…") })
    try {
      let job = await requestTransparency(id, controller.signal, checking ? undefined : props.asset.revision)
      while (job.status === "processing") {
        set({ busy: true, progress: job.progress, text: uxText(`去白底中 ${job.progress}%`, `Processing ${job.progress}%`) })
        await new Promise<void>(resolve => setTimeout(resolve, 700))
        controller.signal.throwIfAborted()
        job = await requestTransparency(id, controller.signal)
      }
      if (job.status === "failed") {
        set({ busy: false, progress: 0, text: job.message || uxText("处理失败，原素材未变。", "Processing failed. The asset is unchanged.") })
      } else {
        if (job.asset) updateUploadedIconAsset(job.asset)
        set({ busy: false, progress: 100, text: job.status === "unchanged"
          ? uxText("未检测到可去除的白底", "No removable white background")
          : uxText("已替换，应用处同步更新", "Replaced; applied icons updated") })
      }
    } catch (error) {
      const rejected = error instanceof TransparencyRequestError && error.status >= 400 && error.status < 500
      set({ busy: false, progress: 0, uncertain: !rejected, text: rejected
        ? error.message
        : uxText("状态暂不可用，后台可能仍在处理。", "Status unavailable; processing may continue in the background.") })
    } finally { workers.delete(id) }
  }
  return <Show when={/\.gif$/i.test(props.asset.name)}>
    <div class="im-transparent-action">
      <button type="button" disabled={props.disabled || state()?.busy} onClick={() => void process()} aria-label={uxText(`生成透明背景：${props.asset.name}`, `Make transparent: ${props.asset.name}`)}>
        {state()?.busy ? state()?.text : state()?.uncertain ? uxText("核对处理状态", "Check status") : uxText("生成透明背景", "Make transparent")}
      </button>
      <Show when={state()?.busy}><progress max="100" value={state()?.progress} aria-label={uxText("去白底进度", "Transparency progress")} /></Show>
      <Show when={state()?.text && !state()?.busy}><small role="status">{state()?.text}</small></Show>
    </div>
  </Show>
}

export function disposeTransparencyWatchers() {
  for (const controller of workers.values()) controller.abort()
  workers.clear()
}
