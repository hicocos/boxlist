import type Artplayer from "artplayer"
import { uxText } from "~/utils/ux"

export interface VideoRecovery {
  reset(): void
  destroy(): void
}

// Own the media error policy instead of Artplayer's canplay-reset retry loop.
export function installVideoRecovery(player: Artplayer): VideoRecovery {
  const video = player.video
  const panel = document.createElement("div")
  panel.className = "video-recovery"
  panel.hidden = true
  const message = document.createElement("p")
  message.setAttribute("role", "status")
  const button = document.createElement("button")
  button.type = "button"
  button.textContent = uxText("手动重试", "Retry manually")
  panel.append(message, button)
  player.template.$player.append(panel)
  let attempts = 0
  let timer: number | undefined
  let resume = false
  let position = 0
  let pendingMetadata: (() => void) | undefined
  let disposed = false

  const cancel = () => {
    clearTimeout(timer)
    timer = undefined
    if (pendingMetadata) video.removeEventListener("loadedmetadata", pendingMetadata)
    pendingMetadata = undefined
  }
  const reload = () => {
    cancel()
    if (disposed) return
    panel.hidden = true
    pendingMetadata = () => {
      pendingMetadata = undefined
      if (position > 0 && Number.isFinite(video.duration)) {
        video.currentTime = Math.min(position, video.duration)
      }
      if (resume) {
        void player.play().catch(() => {
          if (disposed || video.error) return
          message.textContent = uxText("浏览器未允许自动播放，请点击播放继续。", "Autoplay was blocked. Press Play to continue.")
          panel.hidden = false
        })
      }
    }
    video.addEventListener("loadedmetadata", pendingMetadata, { once: true })
    player.url = player.option.url
  }
  const onPlay = () => { resume = true }
  const onPause = () => {
    if (!video.error) resume = false
  }
  const onTime = () => {
    if (!video.error && video.readyState >= 2) position = video.currentTime
  }
  const onError = () => {
    cancel()
    const code = video.error?.code
    player.loading.show = false
    player.controls.show = true
    panel.hidden = false
    if (code === 2 && attempts < 3) {
      attempts++
      message.textContent = uxText(`网络中断，正在重试（${attempts}/3）…`, `Network interrupted. Retrying (${attempts}/3)…`)
      timer = window.setTimeout(reload, 1000 * attempts)
    } else {
      message.textContent = code === 3
        ? uxText("音视频解码失败，已停止自动重试。文件可能损坏或编码不兼容。", "Media decoding failed. Automatic retries stopped. The file may be damaged or incompatible.")
        : code === 4
          ? uxText("视频格式不受支持或资源不可用，已停止自动重试。", "Unsupported format or unavailable media. Automatic retries stopped.")
          : code === 2
            ? uxText("网络重试已达 3 次，请检查网络后手动重试。", "Three network retries failed. Check your connection and retry manually.")
            : uxText("视频加载已中止，请手动重试。", "Media loading stopped. Retry manually.")
    }
  }
  const retry = (event: MouseEvent) => {
    event.stopPropagation()
    attempts = 0
    resume = true
    reload()
  }
  button.addEventListener("click", retry)
  // Remove only the built-in media-error handler, before switchUrl adds its own.
  player.off("video:error")
  player.on("video:error", onError)
  video.addEventListener("play", onPlay)
  video.addEventListener("pause", onPause)
  video.addEventListener("timeupdate", onTime)
  return {
    reset() {
      cancel()
      attempts = 0
      position = 0
      resume = false
      panel.hidden = true
    },
    destroy() {
      disposed = true
      cancel()
      player.off("video:error", onError)
      video.removeEventListener("play", onPlay)
      video.removeEventListener("pause", onPause)
      video.removeEventListener("timeupdate", onTime)
      button.removeEventListener("click", retry)
      panel.remove()
    },
  }
}
