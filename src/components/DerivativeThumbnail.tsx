import { createContext, useContext, createEffect, createSignal, on, onCleanup, Show, splitProps } from "solid-js"
import type { Accessor, JSXElement } from "solid-js"
import { hope, type ImageProps } from "@hope-ui/solid"
import type { Obj } from "~/types"
import { probeDerivative } from "~/utils/derivatives"
import type { Derivatives } from "~/types/derivatives"
import { createDerivativeMedia, type DerivativeStatusResult } from "~/utils/derivative-policy"

export interface ThumbnailScope {
  derivatives: (name: string) => Derivatives | undefined
  refresh: (name: string, signal?: AbortSignal) => Promise<DerivativeStatusResult>
  revision: Accessor<number>
}
export const ThumbnailContext = createContext<ThumbnailScope>()

/** No original fallback in grids/sidebar. Image view explicitly supplies one. */
export function DerivativeThumbnail(props: ImageProps<"img"> & {
  obj: Pick<Obj, "name" | "size" | "thumb" | "derivatives">
  original?: string
  fallbackErr?: JSXElement
  refresh?: ThumbnailScope["refresh"]
}) {
  const scope = useContext(ThumbnailContext)
  const [local, rest] = splitProps(props, ["obj", "original", "fallbackErr", "fallback", "refresh", "src", "align", "fit", "htmlWidth", "htmlHeight", "fallbackSrc", "ignoreFallback"])
  const [src, setSrc] = createSignal<string>()
  const [loadedSrc, setLoadedSrc] = createSignal<string>()
  const loaded = () => !!src() && loadedSrc() === src()
  let image: HTMLImageElement | undefined
  let disposed = false
  const settleCachedImage = () => {
    const url = src()
    if (!disposed && url && image?.isConnected && image.getAttribute("src") === url && image.complete && image.naturalWidth > 0) {
      setLoadedSrc(url)
    }
  }
  // An unchanged src does not fire load again. Inspect actual completion after
  // attribute updates/mount as well, including a memory-cache hit before ref.
  createEffect(on(src, () => { setLoadedSrc(undefined); queueMicrotask(settleCachedImage) }))
  onCleanup(() => { disposed = true })
  let recover: (url: string) => Promise<void> = async () => {}
  createEffect(on(() => [local.obj, local.obj.name, local.obj.thumb, local.obj.size, local.obj.derivatives, local.original, scope?.revision()] as const, () => {
    const controller = new AbortController()
    let alive = true
    const media = createDerivativeMedia({
      profile: "thumb", derivatives: scope?.derivatives(local.obj.name) ?? local.obj.derivatives,
      probe: probeDerivative, upstream: local.obj.thumb, original: local.original, originalBytes: local.obj.size,
      refresh: signal => (local.refresh ?? scope?.refresh)?.(local.obj.name, signal) ?? Promise.resolve({ code: 0 }),
    })
    // A scope/status refresh is not a new image transfer if the URL is equal.
    setSrc(media.source()?.url)
    recover = async url => {
      const next = await media.recover(url, controller.signal)
      if (alive && src() === url) setSrc(next?.url)
    }
    onCleanup(() => { alive = false; controller.abort() })
  }))
  return <Show when={src()} fallback={local.fallbackErr}>
    <Show when={!loaded()}>{local.fallback}</Show>
    <hope.img {...rest} ref={element => { image = element; queueMicrotask(settleCachedImage) }} src={src()} onLoad={settleCachedImage} onError={(event) => {
      const url = event.currentTarget.getAttribute("src") || ""
      if (url !== src()) return
      setLoadedSrc(undefined)
      void recover(url)
    }} />
  </Show>
}
