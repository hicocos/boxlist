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
  requestImage,
} from "./image-requests"
import { useLink, useRouter } from "~/hooks"
import { objStore, password } from "~/store"
import { Obj } from "~/types"
import { ext, fsList, pathDir } from "~/utils"
import "photoswipe/style.css"
import "./image-lite.css"

// Browser-native formats only. Special formats retain the original viewer.
export const nativeImage = (name: string) =>
  /^(jpe?g|jfif|png|gif|webp|svg|avif|bmp|ico)$/.test(ext(name).toLowerCase())

interface Props {
  images?: Obj[]
  navigate?: (name: string) => void
}
const ImageLite = (props: Props) => {
  const { replace, pathname } = useRouter()
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
  const source = () => {
    retry()
    return objStore.raw_url || rawLink(objStore.obj)
  }
  createEffect(() => {
    const directory = pathDir(pathname())
    if (props.images) return
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
          if (loaded() && !gallery) {
            const list = images()
            warmNeighbours(
              currentIndex(),
              list,
              list.map((o, i) =>
                i === currentIndex() ? source() : rawLink(o),
              ),
            )
          }
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
  const neighbourUrls = (index: number, list: Obj[], urls: string[]) => {
    const connection = (
      navigator as Navigator & {
        connection?: { saveData?: boolean; effectiveType?: string }
      }
    ).connection
    return neighbourIndices(index, list, connection).map((i) => urls[i])
  }
  const warmNeighbours = (index: number, list: Obj[], urls: string[]) => {
    const neighbours = neighbourUrls(index, list, urls)
    // Rapid browsing never accumulates requests for old slides.
    requests.retain([urls[index], ...neighbours])
    for (const url of neighbours) void load(url).catch(() => {})
  }
  const open = async () => {
    if (!loaded() || failed() || opening() || gallery) return
    const generation = viewGeneration
    const isViewCurrent = () => !disposed && generation === viewGeneration
    setOpening(true)
    setGalleryError(false)
    const list = [...images()]
    const start = currentIndex()
    const urls = list.map((o, i) => (i === start ? source() : rawLink(o)))
    let pswp: PhotoSwipe | undefined
    let initialized = false
    try {
      const { default: PhotoSwipeCore } = await import("photoswipe")
      if (!isViewCurrent()) return
      let finalIndex = start
      const viewer = new PhotoSwipeCore({
        dataSource: urls.map((src) => ({ src })),
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
      const nativeRequests = new Map<Content, ReturnType<typeof requestImage>>()
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
            requests.forget(urls[index])
            refresh(index)
          }
          const original = document.createElement("a")
          original.href = urls[index]
          original.target = "_blank"
          original.rel = "noopener noreferrer"
          original.textContent = label("打开原图", "Open original")
          original.onclick = (event) => event.stopPropagation()
          actions.append(retry, original)
          node.append(actions)
        }
        return node
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
        void load(urls[i]).then(
          () => {
            if (waiting.delete(content) && content.hasSlide && current())
              refresh(i)
          },
          (error: unknown) => {
            if (!waiting.delete(content) || !content.hasSlide || !current())
              return
            if (
              error instanceof ImageRequestError &&
              error.reason === "cancelled"
            ) {
              // Re-entering a slide can race the cancellation microtask.
              if (i === viewer.currIndex) loadPlaceholder(content)
              return
            }
            errors.set(
              i,
              error instanceof ImageRequestError && error.reason === "timeout"
                ? "timeout"
                : "error",
            )
            refresh(i)
          },
        )
      }
      viewer.addFilter("itemData", (_data: SlideData, index: number) => {
        const known = cache.get(urls[index])
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
      viewer.on("contentActivate", ({ content }) => {
        queueMicrotask(() => loadPlaceholder(content))
      })
      // The visible PhotoSwipe <img> needs its own deadline too: known
      // dimensions don't mean a cached response is still available.
      viewer.on("contentLoadImage", (event) => {
        const { content } = event
        const image = content.element as HTMLImageElement | undefined
        if (!image) return
        event.preventDefault()
        nativeRequests.get(content)?.cancel()
        content.state = "loading"
        image.alt = list[content.index].name
        const task = requestImage(image, urls[content.index])
        nativeRequests.set(content, task)
        void task.promise.then(
          (data) => {
            if (!current() || nativeRequests.get(content) !== task) return
            nativeRequests.delete(content)
            cache.set(data.src, data)
            content.onLoaded()
          },
          (error: unknown) => {
            if (!current() || nativeRequests.get(content) !== task) return
            nativeRequests.delete(content)
            if (
              error instanceof ImageRequestError &&
              error.reason === "cancelled"
            )
              return
            cache.delete(urls[content.index])
            errors.set(
              content.index,
              error instanceof ImageRequestError && error.reason === "timeout"
                ? "timeout"
                : "error",
            )
            content.onError()
          },
        )
      })
      viewer.addFilter("contentErrorElement", (_element, content) =>
        message(content.index, true),
      )
      viewer.on("contentDestroy", ({ content }) => {
        destroyed.add(content)
        waiting.delete(content)
        nativeRequests.get(content)?.cancel()
        nativeRequests.delete(content)
      })
      viewer.on("change", () => {
        finalIndex = viewer.currIndex
        warmNeighbours(finalIndex, list, urls)
        if (viewer.currSlide) loadPlaceholder(viewer.currSlide.content)
      })
      const scroll = window.scrollY
      const overflow = document.body.style.overflow
      document.body.style.overflow = "hidden"
      viewer.on("destroy", () => {
        waiting.clear()
        for (const task of nativeRequests.values()) task.cancel()
        nativeRequests.clear()
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
      <Show keyed when={source() + ":" + retry()}>
        {(_key: string) => {
          const generation = ++viewGeneration
          const url = source()
          let element!: HTMLImageElement
          let task: ReturnType<typeof requestImage> | undefined
          const current = () => !disposed && generation === viewGeneration
          setLoaded(false)
          setFailed("")
          setOpening(false)
          setGalleryError(false)
          requests.retain([])
          onMount(() => {
            task = requestImage(element, url)
            void task.promise.then(
              (data) => {
                if (!current()) return
                cache.set(url, data)
                setLoaded(true)
                const list = images()
                warmNeighbours(
                  currentIndex(),
                  list,
                  list.map((o, i) => (i === currentIndex() ? url : rawLink(o))),
                )
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
              requests.forget(source())
              setRetry((v) => v + 1)
            }}
          >
            {label("重试", "Retry")}
          </button>{" "}
          <a href={source()} target="_blank" rel="noopener noreferrer">
            {label("打开原图", "Open original")}
          </a>
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
