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
import { uxText } from "~/utils/ux"
import "./reader-accessibility.css"
import "./continuous-reader.css"

export interface ReaderItem {
  key: string
  src: string
  name: string
  size: number
}

export interface ContinuousReaderProps {
  items: ReaderItem[]
  /** Reactive navigation commands, not a two-way scroll position binding. */
  command: ReaderCommand
  gap: number
  onProgress: (index: number, fraction: number) => void
  onOpen: (index: number) => void
}

const SEGMENT_SIZE = 30
const MAX_IMAGE_HEIGHT = 100_000
const CONCURRENT_REQUESTS = 2
const LOAD_TIMEOUT = 30_000
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
  const [segmentStart, setSegmentStart] = createSignal(0)
  const segmentEnd = () =>
    Math.min(props.items.length, segmentStart() + SEGMENT_SIZE)
  const entries = createMemo(() =>
    props.items.slice(segmentStart(), segmentEnd()).map((item, offset) => ({
      item,
      index: segmentStart() + offset,
    })),
  )
  // Only measured dimensions survive unmounting. No Image objects/full-directory
  // preloads are cached; browser caching handles revisits to signed source URLs.
  const ratios = new Map<string, number>()
  const slots = new Map<number, Slot>()
  let stack!: HTMLDivElement

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

  const beginLoad = (slot: Slot) => {
    if (disposed || !slot.alive || slot.state !== "queued") return
    const image = new Image()
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
      image.onload = null
      image.onerror = null
      image.removeAttribute("src")
      image.remove()
      release()
      slot.cancel = undefined
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
    image.style.visibility = "hidden"
    image.ondblclick = () => props.onOpen(slot.index)
    image.setAttribute("role", "button")
    image.setAttribute(
      "aria-label",
      `${slot.index + 1}. ${slot.item.name} — ${uxText("单张查看", "View single image")}`,
    )
    image.tabIndex = -1
    image.onkeydown = (event) => {
      if (
        (event.key === "Enter" || event.key === " ") &&
        !event.repeat &&
        !event.altKey &&
        !event.ctrlKey &&
        !event.metaKey &&
        !event.shiftKey
      ) {
        event.preventDefault()
        props.onOpen(slot.index)
      }
    }
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
      setState(slot, "ready")
      release()
      scheduleLayout()
    }
    image.onerror = () => fail("error")
    setState(slot, "loading")
    slot.host.append(image)
    timer = setTimeout(() => fail("timeout"), LOAD_TIMEOUT)
    // Assign src only after the lease and all cleanup callbacks exist.
    image.src = slot.item.src
  }

  function refreshNearby() {
    const margin = Math.max(1000, window.innerHeight)
    for (const slot of slots.values()) {
      const rect = slot.element.getBoundingClientRect()
      slot.near =
        (rect.bottom >= -margin && rect.top <= window.innerHeight + margin) ||
        (pending?.index === slot.index && pending.key === slot.item.key)
      if (!slot.near) {
        slot.cancel?.()
        if (slot.state === "queued") setState(slot, "idle")
      } else if (slot.state === "idle") {
        setState(slot, "queued")
      }
    }
  }

  function pump() {
    const distance = (slot: Slot) => {
      if (pending?.index === slot.index) return -1
      const rect = slot.element.getBoundingClientRect()
      return Math.max(0, rect.top - window.innerHeight, -rect.bottom)
    }
    const queue = [...slots.values()]
      .filter((slot) => slot.near && slot.state === "queued")
      .sort((a, b) => distance(a) - distance(b) || a.index - b.index)
    for (const slot of queue) {
      if (active >= CONCURRENT_REQUESTS) break
      beginLoad(slot)
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
    slots.set(index, slot)
    observer?.observe(element)
    scheduleLayout()
    return () => {
      slot.alive = false
      slot.cancel?.()
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
                          ? "图片加载失败。"
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
