import {
  lazy,
  createEffect,
  createMemo,
  createSignal,
  onCleanup,
  Switch,
  Match,
  Show,
  on,
} from "solid-js"
import { layout } from "~/store"
import { ContextMenu } from "./context-menu"
import { Pager } from "./Pager"
import { useLink, useT } from "~/hooks"
import { objStore, local } from "~/store"
import { ObjType } from "~/types"
import { bus } from "~/utils"
import lightGallery from "lightgallery"
import lgThumbnail from "lightgallery/plugins/thumbnail"
import lgZoom from "lightgallery/plugins/zoom"
import lgAutoplay from "lightgallery/plugins/autoplay"
import lgFullscreen from "lightgallery/plugins/fullscreen"
import "lightgallery/css/lightgallery-bundle.css"
import "./gallery.css"
import { LightGallery } from "lightgallery/lightgallery"
import { Search } from "./Search"
import { Button } from "@hope-ui/solid"
import { ThumbnailContext } from "~/components/DerivativeThumbnail"
import { useDirectoryThumbnails } from "./derivative-thumbnails"
import { createDerivativeMedia, type DerivativeMedia } from "~/utils/derivative-policy"
import { probeDerivative } from "~/utils/derivatives"
import { useFolderVisibility } from "./helper"

const ListLayout = lazy(() => import("./List"))
const GridLayout = lazy(() => import("./Grid"))
const ImageLayout = lazy(() => import("./Images"))

const Folder = () => {
  const { rawLink } = useLink()
  const thumbnails = useDirectoryThumbnails()
  const isVisible = useFolderVisibility()
  const [refreshing, setRefreshing] = createSignal(false)
  const [refreshError, setRefreshError] = createSignal(false)
  const refreshController = new AbortController()
  const refreshVisible = async () => {
    if (refreshing()) return
    setRefreshing(true)
    setRefreshError(false)
    const names = new Set<string>()
    document.querySelectorAll<HTMLElement>(".grid-item[data-name], .image-item[data-name]").forEach(element => {
      const rect = element.getBoundingClientRect()
      if (rect.bottom >= 0 && rect.top <= innerHeight && element.dataset.name) names.add(element.dataset.name)
    })
    try {
      const success = await thumbnails.refreshVisible([...names], refreshController.signal)
      if (!refreshController.signal.aborted) setRefreshError(!success)
    } finally { if (!refreshController.signal.aborted) setRefreshing(false) }
  }
  const images = createMemo(() =>
    objStore.objs.filter((obj) => obj.type === ObjType.IMAGE && isVisible(obj)),
  )

  let dynamicGallery: LightGallery | undefined
  const galleryMedia = new Map<string, DerivativeMedia>()
  let galleryAbort = new AbortController()
  const emptyThumb = "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='80' height='60'%3E%3C/svg%3E"
  const thumbnailError = async (event: Event) => {
    const image = event.target
    if (!(image instanceof HTMLImageElement) || !image.closest(".openlist-folder-gallery .lg-thumb-item")) return
    const url = image.getAttribute("src") || ""
    const media = galleryMedia.get(url)
    if (!media) return
    const controller = galleryAbort
    const next = await media.recover(url, controller.signal)
    if (controller.signal.aborted || !image.isConnected) return
    if (next) galleryMedia.set(next.url, media)
    image.src = next?.url || emptyThumb
  }
  document.addEventListener("error", thumbnailError, true)
  const initGallery = () => {
    galleryAbort.abort()
    galleryAbort = new AbortController()
    galleryMedia.clear()
    dynamicGallery = lightGallery(document.createElement("div"), {
      addClass: "lightgallery-container openlist-folder-gallery",
      dynamic: true,
      thumbnail: local["show_gallery_thumbnails"] === "visible",
      // Remove only rotate/flip; retain zoom, fullscreen and slideshow.
      plugins: [lgZoom, lgThumbnail, lgAutoplay, lgFullscreen],
      actualSize: true,
      showZoomInOutIcons: false,
      closable: true,
      showCloseIcon: true,
      // The library hides the close icon on phones unless explicitly enabled.
      mobileSettings: {
        controls: false,
        showCloseIcon: true,
        download: false,
      },
      dynamicEl: images().map((obj) => {
        const raw = rawLink(obj, true)
        const media = createDerivativeMedia({ profile: "thumb", probe: probeDerivative,
          derivatives: thumbnails.derivatives(obj.name) ?? obj.derivatives,
          upstream: obj.thumb, refresh: signal => thumbnails.refresh(obj.name, signal) })
        const thumb = media.source()?.url
        if (thumb) galleryMedia.set(thumb, media)
        return {
          src: raw,
          thumb: thumb || emptyThumb,
          subHtml: `<h4>${obj.name.replace(/[&<>"']/g, c => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" } as Record<string, string>)[c])}</h4>`,
        }
      }),
    })
  }
  createEffect(
    on([images, thumbnails.revision, () => local["show_gallery_thumbnails"]], () => {
      galleryAbort.abort()
      dynamicGallery?.destroy()
      dynamicGallery = undefined
    }),
  )
  bus.on("gallery", (name) => {
    if (!dynamicGallery) {
      initGallery()
    }
    dynamicGallery?.openGallery(images().findIndex((obj) => obj.name === name))
  })
  onCleanup(() => {
    bus.off("gallery")
    refreshController.abort()
    galleryAbort.abort()
    document.removeEventListener("error", thumbnailError, true)
    dynamicGallery?.destroy()
  })
  const t = useT()
  return (
    <ThumbnailContext.Provider value={thumbnails}>
      <Show when={layout() === "grid" || layout() === "image"}><div style={{ width: "100%", display: "flex", "flex-wrap": "wrap", gap: "8px" }}>
        <Button size="sm" loading={refreshing()} onClick={refreshVisible}>{t("derivatives.refresh_visible")}</Button>
        <span role="status">{refreshError() ? t("derivatives.refresh_failed") : ""}</span>
      </div></Show>
      <Switch>
        <Match when={layout() === "list"}>
          <ListLayout />
        </Match>
        <Match when={layout() === "grid"}>
          <GridLayout />
        </Match>
        <Match when={layout() === "image"}>
          <ImageLayout images={images()} />
        </Match>
      </Switch>
      <Pager />
      <Search />
      <ContextMenu />
    </ThumbnailContext.Provider>
  )
}

export default Folder
