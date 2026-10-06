import {
  batch,
  createEffect,
  createSignal,
  on,
  onCleanup,
  Show,
} from "solid-js"
import type { ReaderImage } from "./reader-utils"

export default function SingleReader(props: {
  items: ReaderImage[]
  index: number
  onChange: (index: number) => void
}) {
  const [zoom, setZoom] = createSignal(false)
  const [playing, setPlaying] = createSignal(false)
  const [state, setState] = createSignal("loading")
  const [retry, setRetry] = createSignal(0)
  const [elapsed, setElapsed] = createSignal(0)
  const [ended, setEnded] = createSignal(false)
  const slideDuration = 4000
  let stage!: HTMLDivElement
  let timer: ReturnType<typeof setInterval> | undefined
  let pointer: { x: number; y: number; id: number } | undefined
  const move = (delta: number) => {
    const next = props.index + delta
    if (next >= 0 && next < props.items.length) props.onChange(next)
    else setPlaying(false)
  }
  createEffect(() => {
    props.items[props.index]?.key
    setZoom(false)
    stage?.scrollTo(0, 0)
  })
  createEffect(
    on([playing, state, () => props.items[props.index]?.key], () => {
      clearInterval(timer)
      if (!playing() || state() !== "ready") return
      // A single clock drives both feedback and slide advance; loading is excluded.
      const started = performance.now() - elapsed()
      timer = setInterval(() => {
        if (document.hidden) {
          setPlaying(false)
          return
        }
        const value = Math.min(slideDuration, performance.now() - started)
        batch(() => {
          setElapsed(value)
          if (value >= slideDuration) {
            clearInterval(timer)
            if (props.index >= props.items.length - 1) {
              setEnded(true)
              setPlaying(false)
            } else move(1)
          }
        })
      }, 50)
    }),
  )
  const visibility = () => {
    if (document.hidden) setPlaying(false)
  }
  document.addEventListener("visibilitychange", visibility)
  onCleanup(() => {
    clearInterval(timer)
    document.removeEventListener("visibilitychange", visibility)
  })
  const keydown = (event: KeyboardEvent) => {
    if (/INPUT|SELECT|TEXTAREA/.test((event.target as HTMLElement).tagName))
      return
    if (event.key === "ArrowLeft") move(-1)
    if (event.key === "ArrowRight") move(1)
  }
  document.addEventListener("keydown", keydown)
  onCleanup(() => document.removeEventListener("keydown", keydown))
  return (
    <section class="reader-single" aria-label="单张阅读">
      <div class="reader-single-tools">
        <button
          type="button"
          aria-label="上一张"
          disabled={props.index === 0}
          onClick={() => move(-1)}
        >
          ‹
        </button>
        <button
          type="button"
          aria-pressed={zoom()}
          onClick={() => setZoom(!zoom())}
        >
          {zoom() ? "适应屏幕" : "放大原图"}
        </button>
        <button
          type="button"
          aria-pressed={playing()}
          disabled={props.items.length <= 1}
          onClick={() => {
            if (playing()) {
              setPlaying(false)
              return
            }
            if (props.items.length <= 1) return
            batch(() => {
              if (ended() || props.index >= props.items.length - 1) {
                setEnded(false)
                setElapsed(0)
                props.onChange(0)
              }
              setPlaying(true)
            })
          }}
        >
          {props.items.length <= 1
            ? "仅一张"
            : playing()
              ? "暂停"
              : ended() || props.index >= props.items.length - 1
                ? "从头播放"
                : "播放"}
        </button>
        <button
          type="button"
          aria-label="下一张"
          disabled={props.index >= props.items.length - 1}
          onClick={() => move(1)}
        >
          ›
        </button>
      </div>
      <Show when={playing()}>
        <div class="reader-playback" aria-label="自动播放进度">
          <span class="reader-playback-label">
            {state() === "ready"
              ? `${(Math.ceil((slideDuration - elapsed()) / 100) / 10).toFixed(1)} 秒后${props.index === props.items.length - 1 ? "结束" : "下一张"}`
              : "正在加载下一张…"}
          </span>
          <div
            class="reader-playback-track"
            role="progressbar"
            aria-label="当前图片播放进度"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round((elapsed() / slideDuration) * 100)}
          >
            <div
              class="reader-playback-fill"
              style={{ transform: `scaleX(${elapsed() / slideDuration})` }}
            />
          </div>
        </div>
      </Show>
      <div
        class="reader-single-stage"
        classList={{ "is-zoomed": zoom() }}
        ref={stage}
        onPointerDown={(e) => {
          if (!e.isPrimary || zoom()) {
            pointer = undefined
            return
          }
          pointer = { x: e.clientX, y: e.clientY, id: e.pointerId }
        }}
        onPointerCancel={() => {
          pointer = undefined
        }}
        onPointerUp={(e) => {
          if (!pointer || pointer.id !== e.pointerId || zoom()) return
          const dx = e.clientX - pointer.x,
            dy = e.clientY - pointer.y
          pointer = undefined
          if (Math.abs(dx) > 70 && Math.abs(dx) > Math.abs(dy) * 2)
            move(dx < 0 ? 1 : -1)
        }}
      >
        <Show keyed when={props.items[props.index]?.key + ":" + retry()}>
          {(_key: string) => {
            const image = props.items[props.index]
            // Reset at the keyed image mount, before assigning src. A late
            // effect must not overwrite load events for a cached next image.
            batch(() => {
              setState("loading")
              setElapsed(0)
              setEnded(false)
            })
            let active = true
            let timeout: ReturnType<typeof setTimeout> | undefined
            const expire = () => {
              setState("error")
              setPlaying(false)
            }
            timeout = setTimeout(expire, 45000)
            onCleanup(() => {
              active = false
              clearTimeout(timeout)
            })
            return (
              <img
                src={image.src}
                alt={image.name}
                decoding="async"
                draggable={false}
                onLoad={() => {
                  if (!active) return
                  clearTimeout(timeout)
                  setState("ready")
                }}
                onError={() => {
                  if (!active) return
                  clearTimeout(timeout)
                  expire()
                }}
                onDblClick={() => setZoom(!zoom())}
              />
            )
          }}
        </Show>
        <Show when={state() === "loading"}>
          <span class="reader-status" role="status">
            正在加载原图…
          </span>
        </Show>
      </div>
      <Show when={state() === "error"}>
        <p role="alert">
          图片加载失败或超时。
          <button
            type="button"
            onClick={() => {
              setState("loading")
              setRetry(retry() + 1)
            }}
          >
            重试
          </button>
          <a
            href={props.items[props.index]?.src}
            target="_blank"
            rel="noopener noreferrer"
          >
            打开原图
          </a>
        </p>
      </Show>
      <p class="reader-single-name">
        <span class="reader-single-counter">
          {props.index + 1} / {props.items.length}
        </span>{" "}
        · {props.items[props.index]?.name}
      </p>
    </section>
  )
}
