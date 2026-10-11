import {
  For,
  Show,
  createEffect,
  createMemo,
  createSignal,
  on,
  onCleanup,
  onMount,
} from "solid-js"
import type { Setter } from "solid-js"
import type { ReaderCommand } from "./reader-command"
import {
  planReaderLoads,
  readerImageTimeout,
  type ReaderConnection,
} from "./reader-loading-policy"
import { ReaderReadyCache, readerCacheKey, readerCacheLimits, readerImageCost } from "./reader-ready-cache"
import { uxText } from "~/utils/ux"
import type { DerivativeMedia, ReaderQuality, MediaSource } from "~/utils/derivative-policy"
import { useT } from "~/hooks"
import "./reader-accessibility.css"
import "./continuous-reader.css"

export interface ReaderItem {
  key: string
  src: string
  name: string
  size: number
  media?: DerivativeMedia
}

export interface ContinuousReaderProps {
  items: ReaderItem[]
  quality?: ReaderQuality
  /** Reactive navigation commands, not a two-way scroll position binding. */
  command: ReaderCommand
  gap: number
  onProgress: (index: number, fraction: number) => void
  onOpen: (index: number) => void
}

const SEGMENT_SIZE = 30
const MAX_IMAGE_HEIGHT = 100_000
const CONCURRENT_REQUESTS = 2

const ESTIMATED_RATIO = 1.5

type PanelState = "idle" | "queued" | "loading" | "ready" | "error" | "timeout"
interface Slot {
  item: ReaderItem
  index: number
  element: HTMLElement
  host: HTMLDivElement
  setState: Setter<PanelState>
  state: PanelState
  alive: boolean
  near: boolean
  request: number
  image?: HTMLImageElement
  loadedUrl?: string
  cacheKey?: string
  cancel?: () => void
}
interface Anchor {
  index: number
  key: string
  fraction: number
  viewportY: number
}
const fractionOf = (value: number) =>
  Number.isFinite(value) ? Math.min(1, Math.max(0, value)) : 0

/** Document-scrolling reader: no wheel/touch interception or nested scroller. */
export default function ContinuousReader(props: ContinuousReaderProps) {
  const t = useT()
  const source = (item: ReaderItem): MediaSource | undefined => item.media ? item.media.source(props.quality) : { url: item.src, bytes: item.size, kind: "original" }
  const [segmentStart, setSegmentStart] = createSignal(0)
  const segmentEnd = () =>
    Math.min(props.items.length, segmentStart() + SEGMENT_SIZE)
  const entries = createMemo(() =>
    props.items.slice(segmentStart(), segmentEnd()).map((item, offset) => ({
      item,
      index: segmentStart() + offset,
    })),
  )
  // Retain loaded image elements across scrolling and segment changes. URLs
  // and item owners remain exact; never share tickets/pixels across directories.
  const ratios = new Map<string, number>()
  const slots = new Map<number, Slot>()
  let stack!: HTMLDivElement
  interface ReadyImage { image: HTMLImageElement; item: ReaderItem; url: string; slot?: Slot }
  const cacheLimits = readerCacheLimits(
    window.matchMedia("(pointer: coarse)").matches || window.innerWidth < 768,
    (navigator as Navigator & { deviceMemory?: number }).deviceMemory,
  )
  const readyCache = new ReaderReadyCache<ReadyImage>(cacheLimits, (entry) => {
    const slot = entry.slot
    if (slot?.image === entry.image) {
      slot.image = undefined
      slot.loadedUrl = undefined
      slot.cacheKey = undefined
      if (slot.alive) setState(slot, "idle")
    }
    entry.slot = undefined
    entry.image.onload = entry.image.onerror = null
    entry.image.ondblclick = entry.image.onkeydown = null
    entry.image.removeAttribute("src")
    entry.image.remove()
  })
  const detachReady = (slot: Slot) => {
    if (slot.state !== "ready" || !slot.image) return
    const entry = slot.cacheKey ? readyCache.peek(slot.cacheKey) : undefined
    slot.image.remove()
    if (entry?.image === slot.image) {
      entry.slot = undefined
      entry.image.ondblclick = entry.image.onkeydown = null
    } else {
      slot.image.removeAttribute("src")
    }
    slot.image = undefined
    slot.loadedUrl = undefined
    slot.cacheKey = undefined
    if (slot.alive) setState(slot, "idle")
  }
  const trimReady = () => {
    const protectedKeys = new Set<string>()
    for (const slot of slots.values()) {
      if (slot.near && slot.cacheKey) {
        protectedKeys.add(slot.cacheKey)
        readyCache.get(slot.cacheKey)
      }
    }
    readyCache.trim(protectedKeys)
  }
  const bindImageActions = (image: HTMLImageElement, slot: Slot) => {
    image.ondblclick = () => props.onOpen(slot.index)
    image.onkeydown = (event) => {
      if ((event.key === "Enter" || event.key === " ") && !event.repeat &&
        !event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey) {
        event.preventDefault()
        props.onOpen(slot.index)
      }
    }
  }
  const restoreReady = (slot: Slot): boolean => {
    const selected = source(slot.item)
    if (!selected) return false
    const key = readerCacheKey(slot.item.key, selected.url)
    const entry = readyCache.get(key)
    if (!entry) return false
    if (entry.item !== slot.item || !entry.image.complete || !entry.image.naturalWidth) {
      readyCache.delete(key)
      return false
    }
    entry.slot = slot
    slot.image = entry.image
    slot.loadedUrl = entry.url
    slot.cacheKey = key
    bindImageActions(entry.image, slot)
    slot.host.append(entry.image)
    ratios.set(slot.item.key, entry.image.naturalHeight / entry.image.naturalWidth)
    setState(slot, "ready")
    scheduleLayout()
    return true
  }


  let observer: IntersectionObserver | undefined
  let resizeObserver: ResizeObserver | undefined
  let disposed = false
  let mounted = false
  let active = 0
  let width = 0
  let desiredGap = 0
  let layoutFrame = 0
  let workFrame = 0
  let settleFrame = 0
  let progressTimer: ReturnType<typeof setTimeout> | undefined
  let pending: Anchor | undefined
  let navigation = 0
  let lastScrollY = 0
  let scrollDirection = 1
  const connection = () =>
    (navigator as Navigator & { connection?: ReaderConnection & EventTarget })
      .connection

  const setState = (slot: Slot, state: PanelState) => {
    slot.state = state
    if (slot.alive) slot.setState(state)
  }
  const orderedSlots = () =>
    [...slots.values()].sort((a, b) => a.index - b.index)
  // Reading controls are in normal flow, so the entire viewport is available.
  const viewportTop = () => 0

  const readAnchor = (): Anchor | undefined => {
    const top = viewportTop()
    const ordered = orderedSlots()
    const last = ordered[ordered.length - 1]
    if (
      last &&
      window.scrollY + window.innerHeight >=
        document.documentElement.scrollHeight - 2
    ) {
      const rect = last.element.getBoundingClientRect()
      // Short final panels cannot reach the top without artificial blank space.
      // Treat a fully visible final panel at the document bottom as current.
      if (rect.top >= 0 && rect.bottom <= window.innerHeight + 1)
        return {
          index: last.index,
          key: last.item.key,
          fraction: 0,
          viewportY: rect.top,
        }
    }
    for (const slot of ordered) {
      const rect = slot.element.getBoundingClientRect()
      // Ignore the previous panel's subpixel bottom edge after a precise jump.
      if (rect.bottom <= top + 1) continue
      if (rect.top >= window.innerHeight) return
      return {
        index: slot.index,
        key: slot.item.key,
        fraction: fractionOf((top - rect.top) / Math.max(1, rect.height)),
        viewportY: Math.max(top, rect.top),
      }
    }
  }

  const restoreAnchor = (anchor: Anchor | undefined) => {
    if (!anchor) return
    if (anchor === pending && anchor.index === 0 && anchor.fraction === 0) {
      window.scrollTo({ top: 0, behavior: "instant" })
      return
    }
    const slot = slots.get(anchor.index)
    if (!slot || slot.item.key !== anchor.key) return
    const rect = slot.element.getBoundingClientRect()
    const viewportY = anchor === pending ? viewportTop() : anchor.viewportY
    const delta = rect.top + rect.height * anchor.fraction - viewportY
    if (Math.abs(delta) > 0.5) {
      // `instant` also overrides any smooth-scroll style outside this component.
      window.scrollTo({ top: window.scrollY + delta, behavior: "instant" })
    }
  }

  const reportProgress = () => {
    if (disposed || pending || layoutFrame) return
    const anchor = readAnchor()
    if (anchor) props.onProgress(anchor.index, anchor.fraction)
  }
  const scheduleProgress = () => {
    if (disposed || progressTimer !== undefined) return
    progressTimer = setTimeout(() => {
      progressTimer = undefined
      reportProgress()
    }, 120)
  }

  const scheduleWork = () => {
    if (disposed || !mounted || workFrame) return
    workFrame = requestAnimationFrame(() => {
      workFrame = 0
      // Do not launch requests at the old position while a jump/layout is queued.
      if (layoutFrame) {
        scheduleWork()
        return
      }
      refreshNearby()
      pump()
    })
  }

  const finishPending = () => {
    if (!pending || settleFrame) return
    const target = slots.get(pending.index)
    if (!target || !["ready", "error", "timeout"].includes(target.state)) return
    const version = navigation
    settleFrame = requestAnimationFrame(() => {
      settleFrame = 0
      if (disposed || version !== navigation || !pending) return
      if (layoutFrame) return // The layout pass will reschedule this settlement.
      restoreAnchor(pending)
      pending = undefined
      scheduleWork()
      scheduleProgress()
    })
  }

  const scheduleLayout = () => {
    if (disposed || !mounted || layoutFrame) return
    layoutFrame = requestAnimationFrame(() => {
      layoutFrame = 0
      // Read BEFORE changing heights/gaps, including in ResizeObserver callbacks:
      // slots have explicit pixel heights, so width changes have not reflowed them.
      const anchor = pending || readAnchor()
      width = stack.clientWidth
      stack.style.gap = `${desiredGap}px`
      for (const slot of slots.values()) {
        const ratio = ratios.get(slot.item.key) || ESTIMATED_RATIO
        const imageWidth = Math.min(width, MAX_IMAGE_HEIGHT / ratio)
        slot.element.style.height = `${imageWidth * ratio}px`
        slot.host.style.width = `${imageWidth}px`
      }
      restoreAnchor(anchor)
      scheduleWork()
      finishPending()
      scheduleProgress()
    })
  }

  const beginLoad = (slot: Slot, highPriority: boolean) => {
    if (disposed || !slot.alive || slot.state !== "queued") return
    if (restoreReady(slot)) return
    const selected = source(slot.item)
    if (!selected) { setState(slot, "error"); finishPending(); return }
    const controller = new AbortController()
    slot.loadedUrl = selected.url
    const image = new Image()
    slot.image = image
    const request = ++slot.request
    let released = false
    let timer: ReturnType<typeof setTimeout> | undefined
    active++
    const current = () => !disposed && slot.alive && slot.request === request
    const release = () => {
      if (released) return
      released = true
      if (timer !== undefined) clearTimeout(timer)
      active--
      scheduleWork()
    }
    // Each request owns an idempotent lease. Cancelling, timing out, and disposing
    // release it without relying on load/error events from a removed <img>.
    slot.cancel = () => {
      slot.request++
      controller.abort()
      image.onload = null
      image.onerror = null
      image.removeAttribute("src")
      image.remove()
      slot.image = undefined
      release()
      slot.cancel = undefined
      slot.loadedUrl = undefined
      slot.cacheKey = undefined
      if (slot.alive) setState(slot, "idle")
    }
    const fail = (state: "error" | "timeout") => {
      if (!current()) return
      slot.cancel?.()
      setState(slot, state)
      scheduleLayout()
    }
    image.alt = slot.item.name
    image.className = "continuous-reader__image"
    image.decoding = "async"
    image.loading = "eager"
    image.fetchPriority = highPriority ? "high" : "low"
    image.style.visibility = "hidden"
    bindImageActions(image, slot)
    image.setAttribute("role", "button")
    image.setAttribute(
      "aria-label",
      `${slot.index + 1}. ${slot.item.name} — ${uxText("单张查看", "View single image")}`,
    )
    image.tabIndex = -1
    image.onload = () => {
      if (!current()) return
      const ratio = image.naturalHeight / image.naturalWidth
      if (!Number.isFinite(ratio) || ratio <= 0) {
        fail("error")
        return
      }
      image.onload = null
      image.onerror = null
      ratios.set(slot.item.key, ratio)
      image.style.visibility = "visible"
      image.tabIndex = 0
      // The transfer lease ends here. Never leave an abort callback attached
      // to a finished image: leaving the near window must not clear its src.
      slot.cancel = undefined
      slot.cacheKey = readerCacheKey(slot.item.key, selected.url)
      readyCache.put(slot.cacheKey, { image, item: slot.item, url: selected.url, slot },
        readerImageCost(image.naturalWidth, image.naturalHeight, selected.bytes))
      setState(slot, "ready")
      release()
      trimReady()
      scheduleLayout()
    }
    image.onerror = async () => {
      if (!current()) return
      if (timer !== undefined) clearTimeout(timer)
      const next = await slot.item.media?.recover(selected.url, controller.signal)
      if (!current()) return
      if (!next) { fail("error"); return }
      // Recovery returns to the existing byte-aware scheduler; never start an
      // original fallback underneath a preview-sized concurrency reservation.
      slot.cancel?.()
      scheduleWork()
    }
    setState(slot, "loading")
    slot.host.append(image)
    timer = setTimeout(
      () => fail("timeout"),
      readerImageTimeout(selected.bytes, connection()),
    )
    // Assign src only after the lease and all cleanup callbacks exist.
    image.src = selected.url
  }

  function refreshNearby() {
    const margin = Math.max(1000, window.innerHeight)
    for (const slot of slots.values()) {
      const rect = slot.element.getBoundingClientRect()
      slot.near =
        (rect.bottom >= -margin && rect.top <= window.innerHeight + margin) ||
        (pending?.index === slot.index && pending.key === slot.item.key)
      if (!slot.near) {
        if (slot.state === "loading") slot.cancel?.()
        if (slot.state === "queued") setState(slot, "idle")
      } else if (slot.state === "idle") {
        if (!restoreReady(slot)) setState(slot, "queued")
      }
    }
    trimReady()
  }

  function pump() {
    const candidates = [...slots.values()]
      .filter(
        (slot) =>
          slot.near && (slot.state === "queued" || slot.state === "loading"),
      )
      .map((slot) => {
        const rect = slot.element.getBoundingClientRect()
        return {
          index: slot.index,
          state: slot.state as "queued" | "loading",
          visible: rect.bottom > 0 && rect.top < window.innerHeight,
          distance: Math.max(0, rect.top - window.innerHeight, -rect.bottom),
          ahead:
            scrollDirection > 0
              ? rect.top >= 0
              : rect.bottom <= window.innerHeight,
          size: source(slot.item)?.bytes ?? slot.item.size,
        }
      })
    const plan = planReaderLoads(candidates, pending?.index, connection())
    for (const index of plan.cancel) {
      const slot = slots.get(index)
      // Only abort unfinished transfers. Finished images have an independent
      // bounded LRU lifetime, not the near-window request lifetime.
      if (slot?.state === "loading") slot.cancel?.()
    }
    for (const slot of slots.values()) {
      if (slot.state === "loading" && slot.image) {
        slot.image.fetchPriority = plan.highPriority.includes(slot.index)
          ? "high"
          : "low"
      }
    }
    for (const index of plan.start) {
      if (active >= CONCURRENT_REQUESTS) break
      const slot = slots.get(index)
      if (slot) beginLoad(slot, plan.highPriority.includes(index))
    }
  }

  const jump = (index: number, fraction: number) => {
    navigation++
    if (settleFrame) cancelAnimationFrame(settleFrame)
    settleFrame = 0
    if (!props.items.length) {
      pending = undefined
      setSegmentStart(0)
      return
    }
    const target = Math.max(
      0,
      Math.min(
        props.items.length - 1,
        Number.isFinite(index) ? Math.trunc(index) : 0,
      ),
    )
    pending = {
      index: target,
      key: props.items[target].key,
      fraction: fractionOf(fraction),
      viewportY: 0,
    }
    setSegmentStart(Math.floor(target / SEGMENT_SIZE) * SEGMENT_SIZE)
    scheduleLayout()
  }

  createEffect(on(() => props.items, () => {
    // A directory/auth refresh supplies new item owners. Clear old pixels even
    // when filenames or signed URLs happen to match; never make a global cache.
    for (const slot of slots.values()) {
      slot.cancel?.()
      detachReady(slot)
      if (slot.alive) setState(slot, "idle")
    }
    readyCache.clear()
    ratios.clear()
    scheduleLayout()
  }, { defer: true }))

  // `on` keeps internal geometry/status reads out of the command dependencies.
  createEffect(
    on(
      () => [props.items, props.command] as const,
      ([, command]) => jump(command.index, command.fraction),
    ),
  )
  createEffect(() => {
    desiredGap = props.gap === 4 ? 4 : 0
    scheduleLayout()
  })

  createEffect(on(() => props.quality, () => {
    const anchor = pending || readAnchor()
    for (const slot of slots.values()) {
      if (slot.loadedUrl !== source(slot.item)?.url) {
        slot.cancel?.()
        detachReady(slot)
        setState(slot, "idle")
      }
    }
    // Keys, segments and measured geometry survive a quality change. Only the
    // current neighborhood is scheduled, not a new book or a directory refresh.
    restoreAnchor(anchor)
    scheduleWork()
  }, { defer: true }))

  const register = (
    item: ReaderItem,
    index: number,
    element: HTMLElement,
    host: HTMLDivElement,
    setPanelState: Setter<PanelState>,
  ) => {
    const slot: Slot = {
      item,
      index,
      element,
      host,
      setState: setPanelState,
      state: "idle",
      alive: true,
      near: false,
      request: 0,
    }
    const selected = source(item)
    if (!ratios.has(item.key) && selected && "width" in selected && "height" in selected && selected.width && selected.height) {
      ratios.set(item.key, selected.height / selected.width)
    }
    slots.set(index, slot)
    observer?.observe(element)
    scheduleLayout()
    return () => {
      slot.alive = false
      slot.cancel?.()
      detachReady(slot)
      observer?.unobserve(element)
      if (slots.get(index) === slot) slots.delete(index)
    }
  }

  const retry = (index: number) => {
    const slot = slots.get(index)
    if (!slot || !["error", "timeout"].includes(slot.state)) return
    setState(slot, "idle")
    scheduleWork()
  }

  const onScroll = () => {
    const delta = window.scrollY - lastScrollY
    if (Math.abs(delta) > 1) scrollDirection = delta > 0 ? 1 : -1
    lastScrollY = window.scrollY
    scheduleWork()
    scheduleProgress()
  }
  const onResize = () => scheduleLayout()
  // User intent wins over a slow resume/jump. These listeners are passive and
  // never preventDefault; native pinch, pull-to-refresh and document scroll work.
  const interruptNavigation = () => {
    if (!pending) return
    navigation++
    pending = undefined
    if (settleFrame) cancelAnimationFrame(settleFrame)
    settleFrame = 0
    scheduleWork()
    scheduleProgress()
  }
  const onKeyDown = (event: KeyboardEvent) => {
    const target = event.target
    if (
      target instanceof HTMLElement &&
      (target.isContentEditable ||
        /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName))
    )
      return
    if (
      [
        "ArrowUp",
        "ArrowDown",
        "PageUp",
        "PageDown",
        "Home",
        "End",
        " ",
      ].includes(event.key)
    ) {
      interruptNavigation()
    }
  }

  onMount(() => {
    mounted = true
    lastScrollY = window.scrollY
    connection()?.addEventListener("change", scheduleWork)

    if (typeof IntersectionObserver !== "undefined") {
      observer = new IntersectionObserver(() => scheduleWork(), {
        rootMargin: "1000px 0px",
      })
      for (const slot of slots.values()) observer.observe(slot.element)
    }
    if (typeof ResizeObserver !== "undefined") {
      resizeObserver = new ResizeObserver(() => {
        if (Math.abs(stack.clientWidth - width) > 0.5) scheduleLayout()
      })
      resizeObserver.observe(stack)
    }
    window.addEventListener("scroll", onScroll, { passive: true })
    window.addEventListener("resize", onResize, { passive: true })
    window.addEventListener("wheel", interruptNavigation, { passive: true })
    window.addEventListener("touchstart", interruptNavigation, {
      passive: true,
    })
    window.addEventListener("pointerdown", interruptNavigation, {
      passive: true,
    })
    window.addEventListener("keydown", onKeyDown)
    scheduleLayout()
  })
  onCleanup(() => {
    disposed = true
    connection()?.removeEventListener("change", scheduleWork)
    observer?.disconnect()
    resizeObserver?.disconnect()
    cancelAnimationFrame(layoutFrame)
    cancelAnimationFrame(workFrame)
    cancelAnimationFrame(settleFrame)
    if (progressTimer !== undefined) clearTimeout(progressTimer)
    window.removeEventListener("scroll", onScroll)
    window.removeEventListener("resize", onResize)
    window.removeEventListener("wheel", interruptNavigation)
    window.removeEventListener("touchstart", interruptNavigation)
    window.removeEventListener("pointerdown", interruptNavigation)
    window.removeEventListener("keydown", onKeyDown)
    for (const slot of slots.values()) {
      slot.alive = false
      slot.cancel?.()
    }
    slots.clear()
    readyCache.clear()
    ratios.clear()
  })

  const SegmentNavigation = () => (
    <nav class="continuous-reader__navigation" aria-label="阅读分段">
      <button
        type="button"
        disabled={segmentStart() === 0}
        onClick={() => jump(segmentStart() - SEGMENT_SIZE, 0)}
      >
        上一段
      </button>
      <span class="continuous-reader__range" aria-live="polite">
        {props.items.length ? segmentStart() + 1 : 0}–{segmentEnd()} /{" "}
        {props.items.length}
      </span>
      <button
        type="button"
        disabled={segmentEnd() >= props.items.length}
        onClick={() => jump(segmentEnd(), 0)}
      >
        下一段
      </button>
    </nav>
  )

  return (
    <section class="continuous-reader" aria-label="纵向连读">
      <Show when={props.items.length > SEGMENT_SIZE}>
        <SegmentNavigation />
      </Show>
      <div class="continuous-reader__stack" ref={stack}>
        <For each={entries()}>
          {({ item, index }) => {
            const [state, setPanelState] = createSignal<PanelState>("idle")
            let element!: HTMLDivElement
            let host!: HTMLDivElement
            let unregister: (() => void) | undefined
            onMount(() => {
              unregister = register(item, index, element, host, setPanelState)
            })
            onCleanup(() => unregister?.())
            return (
              <div
                ref={element}
                class="continuous-reader__panel"
                data-reader-index={index}
                data-reader-state={state()}
                aria-label={`${index + 1}. ${item.name}`}
              >
                <div class="continuous-reader__image-host" ref={host} />
                <Show when={state() !== "ready"}>
                  <div class="continuous-reader__placeholder">
                    <div class="continuous-reader__placeholder-content">
                      <span class="continuous-reader__name">
                        {index + 1}. {item.name}
                      </span>
                      <span
                        role={
                          state() === "error" || state() === "timeout"
                            ? "status"
                            : undefined
                        }
                      >
                        {state() === "error"
                          ? item.media?.blocked() ? t("derivatives.access_denied") : "图片加载失败。"
                          : state() === "timeout"
                            ? "图片加载超时，可单独重试。"
                            : state() === "loading"
                              ? "正在加载…"
                              : state() === "queued"
                                ? "等待加载…"
                                : "滑动到附近时加载"}
                      </span>
                      <div class="continuous-reader__actions">
                        <Show
                          when={state() === "error" || state() === "timeout"}
                        >
                          <button type="button" onClick={() => retry(index)}>
                            重试
                          </button>
                        </Show>
                        <button
                          type="button"
                          onClick={() => props.onOpen(index)}
                        >
                          单张查看
                        </button>
                      </div>
                    </div>
                  </div>
                </Show>
              </div>
            )
          }}
        </For>
      </div>
      <Show when={!props.items.length}>
        <p class="continuous-reader__empty">此目录没有图片。</p>
      </Show>
      <Show when={props.items.length > SEGMENT_SIZE}>
        <SegmentNavigation />
      </Show>
    </section>
  )
}
