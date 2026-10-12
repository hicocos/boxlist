import {
  createMemo,
  createSignal,
  createEffect,
  onCleanup,
  onMount,
  Show,
} from "solid-js"
import type PhotoSwipe from "photoswipe"
import type { SlideData } from "photoswipe"
import { uxText } from "~/utils/ux"
import {
  createImageRequests,
  ImageRequestError,
  neighbourIndices,
  requestMedia,
  type ImageTask,
} from "./image-requests"
import { useLink, useRouter } from "~/hooks"
import { objStore, password, getHistoryScope } from "~/store"
import { Obj } from "~/types"
import { ext, fsList, pathDir } from "~/utils"
import { createDerivativeMedia, type DerivativeMedia } from "~/utils/derivative-policy"
import { createDerivativeStatusBatch, probeDerivative } from "~/utils/derivatives"
import type { ReaderConnection } from "~/pages/reader/reader-loading-policy"
import "photoswipe/style.css"
import "./image-lite.css"

// Browser-native formats only. Special formats retain the original viewer.
export const nativeImage = (name: string) =>
  /^(jpe?g|jfif|png|gif|webp|svg|avif|bmp|ico)$/.test(ext(name).toLowerCase())

interface Props {
  images?: Obj[]
  navigate?: (name: string) => void
}
interface ViewerImage {
  obj: Obj
  original: string
  media: DerivativeMedia
}
const ImageLite = (props: Props) => {
  const { replace, pathname, isShare } = useRouter()
  const { rawLink } = useLink()
  const label = uxText
  const [siblings, setSiblings] = createSignal<Obj[]>([])
  const images = createMemo(() => {
    const list = (
      props.images || (siblings().length ? siblings() : objStore.objs)
    ).filter((o) => !o.is_dir && nativeImage(o.name))
    return list.some((o) => o.name === objStore.obj.name)
      ? list
      : [objStore.obj]
  })
  const currentIndex = () =>
    Math.max(
      0,
      images().findIndex((o) => o.name === objStore.obj.name),
    )
  const [loaded, setLoaded] = createSignal(false)
  const [failed, setFailed] = createSignal<"" | "error" | "timeout">("")
  const [opening, setOpening] = createSignal(false)
  const [retry, setRetry] = createSignal(0)
  const [galleryError, setGalleryError] = createSignal(false)
  let gallery: PhotoSwipe | undefined
  let disposed = false
  let viewGeneration = 0
  let restoreFrame = 0
  const requests = createImageRequests()
  const { cache, load } = requests
  const connection = () => (navigator as Navigator & { connection?: ReaderConnection }).connection
  const original = () => objStore.raw_url || rawLink(objStore.obj)
  const scope = createMemo(() => {
    getHistoryScope()
    if (props.images) return
    const directory = pathDir(pathname())
    const batch = createDerivativeStatusBatch(isShare() && directory === "/@s" ? pathname() : directory, password())
    onCleanup(() => batch.dispose())
    return batch
  })
  const currentMedia = createMemo(() => {
    const batch = scope()
    const obj = objStore.obj
    const name = isShare() && pathDir(pathname()) === "/@s" ? "" : obj.name
    return createDerivativeMedia({
      profile: "reader", derivatives: batch ? obj.derivatives : undefined,
      original: original(), originalBytes: obj.size, probe: probeDerivative,
      refresh: signal => batch?.refresh(name, signal) ?? Promise.resolve({ code: 0 }),
    })
  })
  const mediaItems = createMemo<ViewerImage[]>(() => {
    const batch = scope()
    return images().map(obj => {
      const originalUrl = obj.name === objStore.obj.name ? original() : rawLink(obj)
      const name = obj.name
      return {
        obj, original: originalUrl,
        media: obj.name === objStore.obj.name ? currentMedia() : createDerivativeMedia({
          profile: "reader", derivatives: batch ? obj.derivatives : undefined,
          original: originalUrl, originalBytes: obj.size, probe: probeDerivative,
          refresh: signal => batch?.refresh(name, signal) ?? Promise.resolve({ code: 0 }),
        }),
      }
    })
  })
  createEffect(() => {
    const directory = pathDir(pathname())
    if (props.images || (isShare() && directory === "/@s")) return
    setSiblings([])
    let cancelled = false
    onCleanup(() => {
      cancelled = true
    })
    // Direct file URLs do not populate objStore.objs. Fetch metadata only,
    // after current image has begun loading; never download an entire gallery.
    void fsList(directory, password())
      .then((resp) => {
        if (!cancelled && !disposed && resp.code === 200) {
          setSiblings(resp.data.content || [])
          if (loaded() && !gallery) warmNeighbours(currentIndex(), mediaItems())
        }
      })
      .catch(() => {})
  })
  const go = (index: number) => {
    const obj = images()[index]
    if (!obj) return
    if (props.navigate) props.navigate(obj.name)
    else replace(obj.name)
  }
  const warmNeighbours = (index: number, items: ViewerImage[]) => {
    const sources = items.map(item => item.media.source())
    const neighbours = neighbourIndices(index, sources, connection(), true)
    requests.retain(neighbours.map(i => sources[i]!.url))
    for (const i of neighbours) void load(sources[i]!, connection()).catch(() => {})
  }
  const open = async () => {
    if (!loaded() || failed() || opening() || gallery) return
    const generation = viewGeneration
    const isViewCurrent = () => !disposed && generation === viewGeneration
    setOpening(true)
    setGalleryError(false)
    const items = mediaItems()
    const list = items.map(item => item.obj)
    const start = currentIndex()
    requests.retain([])
    let pswp: PhotoSwipe | undefined
    let initialized = false
    try {
      const { default: PhotoSwipeCore } = await import("photoswipe")
      if (!isViewCurrent()) return
      let finalIndex = start
      const viewer = new PhotoSwipeCore({
        dataSource: items.map(item => ({ src: item.media.source()?.url })),
        index: start,
        mainClass: "openlist-image-lite-dialog",
        bgOpacity: 1,
        showHideAnimationType: "fade",
        showAnimationDuration: 120,
        hideAnimationDuration: 120,
        preload: [0, 0],
        loop: false,
        allowPanToNext: false,
        wheelToZoom: true,
        zoom: false,
        clickToCloseNonZoomable: false,
        imageClickAction: "toggle-controls",
        tapAction: "toggle-controls",
        doubleTapAction: "zoom",
        closeTitle: label("关闭", "Close"),
        arrowPrevTitle: label("上一张", "Previous"),
        arrowNextTitle: label("下一张", "Next"),
        errorMsg: label("图片加载失败", "Image failed to load"),
        padding: { top: 48, bottom: 16, left: 0, right: 0 },
      })
      pswp = viewer
      gallery = viewer
      const current = () => isViewCurrent() && gallery === viewer
      // Let native links/buttons receive focus and clicks without starting a pan.
      viewer.on("pointerDown", (event) => {
        const target = event.originalEvent.target
        if (
          target instanceof Element &&
          target.closest(".image-lite-slide-actions")
        )
          event.preventDefault()
      })
      type Content = NonNullable<PhotoSwipe["currSlide"]>["content"]
      const errors = new Map<number, "error" | "timeout">()
      const waiting = new Set<Content>()
      const destroyed = new WeakSet<Content>()
      const nativeRequests = new Map<Content, ImageTask>()
      const placeholderRequests = new Map<Content, ImageTask>()
      const refresh = (index: number) => {
        if (current()) viewer.refreshSlideContent(index)
      }
      const message = (index: number, failed = false) => {
        const node = document.createElement("div")
        node.className = "image-lite-slide-message"
        node.setAttribute("role", "status")
        const text = document.createElement("span")
        text.textContent = failed
          ? errors.get(index) === "timeout"
            ? label("图片加载超时", "Image load timed out")
            : label("图片加载失败", "Image failed to load")
          : label("加载图片…", "Loading image…")
        node.append(text)
        if (failed) {
          const actions = document.createElement("div")
          actions.className = "image-lite-slide-actions"
          const retry = document.createElement("button")
          retry.type = "button"
          retry.textContent = label("重试当前图片", "Retry this image")
          retry.onclick = (event) => {
            event.stopPropagation()
            if (!current()) return
            errors.delete(index)
            const source = items[index].media.source()
            if (source) requests.forget(source.url)
            refresh(index)
          }
          const original = document.createElement("a")
          original.href = items[index].original
          original.target = "_blank"
          original.rel = "noopener noreferrer"
          original.textContent = label("下载原图", "Download original")
          original.onclick = (event) => event.stopPropagation()
          actions.append(retry, original)
          node.append(actions)
        }
        return node
      }
      const retainCurrent = (index: number) => {
        requests.retain([])
        for (const [content, task] of placeholderRequests) {
          if (content.index === index) continue
          placeholderRequests.delete(content)
          waiting.delete(content)
          task.cancel()
        }
        for (const [content, task] of nativeRequests) {
          if (content.index === index) continue
          nativeRequests.delete(content)
          task.cancel()
        }
      }
      const loadPlaceholder = (content: Content) => {
        const i = content.index
        // PhotoSwipe creates adjacent holders even with preload disabled. Only
        // the current unknown slide may bypass size/data-saving preload limits.
        if (
          destroyed.has(content) ||
          !content.data.litePending ||
          waiting.has(content) ||
          i !== viewer.currIndex ||
          !current()
        )
          return
        waiting.add(content)
        retainCurrent(i)
        const image = new Image()
        image.fetchPriority = "high"
        const task = requestMedia(image, items[i].media, connection())
        placeholderRequests.set(content, task)
        void task.promise.then(
          data => {
            if (placeholderRequests.get(content) !== task || !current()) return
            placeholderRequests.delete(content)
            cache.set(data.src, data)
            if (waiting.delete(content) && content.hasSlide && i === viewer.currIndex) refresh(i)
          },
          (error: unknown) => {
            if (placeholderRequests.get(content) !== task || !current()) return
            placeholderRequests.delete(content)
            waiting.delete(content)
            if (!content.hasSlide || i !== viewer.currIndex) return
            if (error instanceof ImageRequestError && error.reason === "cancelled") return
            errors.set(i, error instanceof ImageRequestError && error.reason === "timeout" ? "timeout" : "error")
            refresh(i)
          },
        )
      }
      viewer.addFilter("itemData", (_data: SlideData, index: number) => {
        const source = items[index].media.source()
        const known = source && (cache.get(source.url) ||
          (source.width && source.height && source.width > 0 && source.height > 0
            ? { src: source.url, width: source.width, height: source.height }
            : undefined))
        return known && !errors.has(index)
          ? { ...known, alt: list[index].name }
          : {
              html: "",
              litePending: !errors.has(index),
              liteError: errors.has(index),
            }
      })
      viewer.on("contentLoad", (event) => {
        const { content } = event
        if (!content.data.litePending && !content.data.liteError) return
        event.preventDefault()
        const element = document.createElement("div")
        element.className = "pswp__content"
        element.append(message(content.index, !!content.data.liteError))
        content.element = element
        queueMicrotask(() => loadPlaceholder(content))
      })
      const loadNative = (content: Content) => {
        if (!current() || destroyed.has(content) || content.index !== viewer.currIndex ||
            nativeRequests.has(content) || content.data.litePending || content.data.liteError ||
            content.state === "loaded" || content.state === "error") return
        const image = content.element as HTMLImageElement | undefined
        if (!image) return
        retainCurrent(content.index)
        content.state = "loading"
        image.alt = list[content.index].name
        image.fetchPriority = "high"
        const task = requestMedia(image, items[content.index].media, connection())
        nativeRequests.set(content, task)
        void task.promise.then(
          data => {
            if (!current() || nativeRequests.get(content) !== task) return
            nativeRequests.delete(content)
            cache.set(data.src, data)
            if (data.src !== content.data.src) { refresh(content.index); return }
            content.onLoaded()
            if (content.index === viewer.currIndex) warmNeighbours(content.index, items)
          },
          (error: unknown) => {
            if (!current() || nativeRequests.get(content) !== task) return
            nativeRequests.delete(content)
            if (error instanceof ImageRequestError && error.reason === "cancelled") return
            errors.set(content.index, error instanceof ImageRequestError && error.reason === "timeout" ? "timeout" : "error")
            content.onError()
          },
        )
      }
      viewer.on("contentActivate", ({ content }) => {
        queueMicrotask(() => {
          loadPlaceholder(content)
          loadNative(content)
        })
      })
      viewer.on("contentLoadImage", event => {
        event.preventDefault()
        loadNative(event.content)
      })
      viewer.addFilter("contentErrorElement", (_element, content) =>
        message(content.index, true),
      )
      viewer.on("contentDestroy", ({ content }) => {
        destroyed.add(content)
        waiting.delete(content)
        nativeRequests.get(content)?.cancel()
        nativeRequests.delete(content)
        placeholderRequests.get(content)?.cancel()
        placeholderRequests.delete(content)
      })
      viewer.on("change", () => {
        finalIndex = viewer.currIndex
        retainCurrent(finalIndex)
        if (viewer.currSlide) {
          const content = viewer.currSlide.content
          loadPlaceholder(content)
          loadNative(content)
          if (content.state === "loaded") warmNeighbours(finalIndex, items)
        }
      })
      viewer.on("uiRegister", () => {
        viewer.ui?.registerElement({
          name: "original", order: 8, isButton: false, tagName: "a",
          html: label("下载原图", "Download original"),
          onInit: element => {
            const link = element as HTMLAnchorElement
            link.target = "_blank"
            link.rel = "noopener noreferrer"
            link.style.cssText = "color:white;align-self:center;padding:12px;white-space:nowrap"
            link.href = items[start].original
            viewer.on("change", () => { link.href = items[viewer.currIndex].original })
          },
        })
      })
      const scroll = window.scrollY
      const overflow = document.body.style.overflow
      document.body.style.overflow = "hidden"
      viewer.on("destroy", () => {
        waiting.clear()
        for (const task of nativeRequests.values()) task.cancel()
        nativeRequests.clear()
        for (const task of placeholderRequests.values()) task.cancel()
        placeholderRequests.clear()
        requests.retain([])
        if (gallery === viewer) gallery = undefined
        document.body.style.overflow = overflow
        if (initialized && isViewCurrent()) {
          if (finalIndex !== start) go(finalIndex)
          restoreFrame = requestAnimationFrame(() => {
            restoreFrame = 0
            if (!disposed) window.scrollTo(0, scroll)
          })
        }
      })
      viewer.init()
      initialized = true
    } catch {
      pswp?.destroy()
      if (isViewCurrent()) setGalleryError(true)
    } finally {
      if (isViewCurrent()) setOpening(false)
    }
  }
  onCleanup(() => {
    disposed = true
    viewGeneration++
    cancelAnimationFrame(restoreFrame)
    gallery?.destroy()
    requests.dispose()
  })
  return (
    <section
      class="image-lite"
      aria-label={label("图像查看器", "Image Viewer")}
    >
      <div class="image-lite-toolbar">
        <button
          type="button"
          aria-label={label("上一张", "Previous")}
          disabled={currentIndex() === 0}
          onClick={() => go(currentIndex() - 1)}
        >
          <svg
            width="22"
            height="22"
            viewBox="0 0 24 24"
            fill="none"
            aria-hidden="true"
          >
            <path
              d="m14 5-7 7 7 7"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </button>
        <span class="image-lite-name" title={objStore.obj.name}>
          {objStore.obj.name}
        </span>
        <span class="image-lite-counter">
          {currentIndex() + 1} / {images().length}
        </span>
        <button
          type="button"
          aria-label={label("下一张", "Next")}
          disabled={currentIndex() === images().length - 1}
          onClick={() => go(currentIndex() + 1)}
        >
          <svg
            width="22"
            height="22"
            viewBox="0 0 24 24"
            fill="none"
            aria-hidden="true"
          >
            <path
              d="m10 5 7 7-7 7"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </button>
      </div>
      <Show keyed when={{ media: currentMedia(), retry: retry() }}>
        {({ media }) => {
          const generation = ++viewGeneration
          let element!: HTMLImageElement
          let task: ImageTask | undefined
          const current = () => !disposed && generation === viewGeneration
          setLoaded(false)
          setFailed("")
          setOpening(false)
          setGalleryError(false)
          requests.retain([])
          onMount(() => {
            task = requestMedia(element, media, connection())
            void task.promise.then(
              (data) => {
                if (!current()) return
                cache.set(data.src, data)
                setLoaded(true)
                warmNeighbours(currentIndex(), mediaItems())
              },
              (error: unknown) => {
                if (
                  !current() ||
                  (error instanceof ImageRequestError &&
                    error.reason === "cancelled")
                )
                  return
                setLoaded(false)
                setFailed(
                  error instanceof ImageRequestError &&
                    error.reason === "timeout"
                    ? "timeout"
                    : "error",
                )
              },
            )
          })
          onCleanup(() => {
            if (generation === viewGeneration) viewGeneration++
            task?.cancel()
            requests.retain([])
            gallery?.destroy()
          })
          return (
            <button
              type="button"
              class="image-lite-stage"
              aria-label={label("点图放大", "Open immersive viewer")}
              onClick={open}
            >
              <img
                ref={element}
                alt={objStore.obj.name}
                decoding="async"
                fetchpriority="high"
                draggable={false}
              />
              <Show when={!loaded() && !failed()}>
                <span class="image-lite-status" role="status">
                  {label("加载图片…", "Loading image…")}
                </span>
              </Show>
            </button>
          )
        }}
      </Show>
      <Show when={failed()}>
        <div class="image-lite-error" role="alert">
          {failed() === "timeout"
            ? label("图片加载超时", "Image load timed out")
            : label("图片加载失败", "Image failed to load")}{" "}
          <button
            type="button"
            onClick={() => {
              const source = currentMedia().source()
              if (source) requests.forget(source.url)
              setRetry((v) => v + 1)
            }}
          >
            {label("重试", "Retry")}
          </button>
        </div>
      </Show>
      <Show when={galleryError()}>
        <div class="image-lite-error" role="alert">
          {label(
            "查看器加载失败，请再点图片重试",
            "Viewer failed to load. Tap image to retry.",
          )}
        </div>
      </Show>
      <div class="image-lite-hint">
        <a href={original()} target="_blank" rel="noopener noreferrer">
          {label("下载原图", "Download original")}
        </a>{" · "}
        {opening()
          ? label("正在打开…", "Opening…")
          : label(
              "点图放大 · 双指缩放 · 点击左右滑动切图",
              "Tap to enlarge · Pinch to zoom · Swipe to browse",
            )}
      </div>
    </section>
  )
}
export default ImageLite
