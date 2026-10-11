import {
  For,
  Show,
  createEffect,
  createMemo,
  createSignal,
  onCleanup,
  onMount,
} from "solid-js"
import type { ReaderImage } from "./reader-utils"
import { DerivativeThumbnail } from "~/components/DerivativeThumbnail"
import type { DerivativeStatusResult, ReaderQuality } from "~/utils/derivative-policy"
import { useT } from "~/hooks"

export default function ReaderTools(props: {
  items: ReaderImage[]
  index: number
  mode: "continuous" | "single"
  quality: ReaderQuality
  onQuality: (quality: ReaderQuality) => void
  refreshThumbnail: (name: string, signal?: AbortSignal) => Promise<DerivativeStatusResult>
  immersive: boolean
  width: string
  gap: number
  onJump: (index: number) => void
  onMode: (mode: "continuous" | "single") => void
  onImmersive: (value: boolean) => void
  onWidth: (value: string) => void
  onGap: (value: number) => void
  onFullscreen: () => void
}) {
  const t = useT()
  let dialog!: HTMLDialogElement
  let opener: HTMLElement | null = null
  const [panel, setPanel] = createSignal<"settings" | "pages">("settings")
  const [page, setPage] = createSignal(0)
  const [jumpValue, setJumpValue] = createSignal("1")
  const pageSize = 24
  const thumbnails = createMemo(() =>
    props.items.slice(page() * pageSize, (page() + 1) * pageSize),
  )
  createEffect(() => setJumpValue(String(props.index + 1)))
  const open = (value: "settings" | "pages") => {
    opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null
    setPanel(value)
    setPage(Math.floor(props.index / pageSize))
    dialog.showModal()
  }
  const close = () => dialog.close()
  const navigate = (index: number) => {
    close()
    props.onJump(index)
  }
  const keydown = (e: KeyboardEvent) => {
    const target = e.target as HTMLElement
    if (
      e.defaultPrevented ||
      e.altKey ||
      e.ctrlKey ||
      e.metaKey ||
      e.shiftKey ||
      e.repeat ||
      target.isContentEditable ||
      /INPUT|SELECT|TEXTAREA/.test(target.tagName) ||
      dialog.open
    )
      return
    const key = e.key.toLowerCase()
    if (key === "escape" && props.immersive) props.onImmersive(false)
    else if (key === "h") props.onImmersive(!props.immersive)
    else if (key === "g") open("pages")
    else if (key === "s") open("settings")
    else if (key === "f") props.onFullscreen()
    else if (key === "arrowleft") props.onJump(props.index - 1)
    else if (key === "arrowright") props.onJump(props.index + 1)
    else if (key === "home") props.onJump(0)
    else if (key === "end") props.onJump(props.items.length - 1)
    else return
    e.preventDefault()
  }
  onMount(() => document.addEventListener("keydown", keydown))
  onCleanup(() => document.removeEventListener("keydown", keydown))
  return (
    <>
      <nav class="reader-dock" aria-label="阅读导航">
        <Show when={!props.immersive}>
          <button
            type="button"
            aria-label="上一张"
            disabled={props.index === 0}
            onClick={() => props.onJump(props.index - 1)}
          >
            ‹
          </button>
          <button
            class="reader-page-trigger"
            type="button"
            onClick={() => open("pages")}
            aria-haspopup="dialog"
            aria-label="浏览缩略图"
          >
            <span>
              {props.index + 1}{" "}
              <span class="reader-muted">/ {props.items.length}</span>
            </span>
            <small>缩略图</small>
          </button>
          <input
            class="reader-progress-slider"
            type="range"
            min="1"
            max={props.items.length}
            value={props.index + 1}
            aria-label="阅读进度"
            aria-valuetext={`第 ${props.index + 1} 张，共 ${props.items.length} 张`}
            onInput={(e) => props.onJump(Number(e.currentTarget.value) - 1)}
          />
          <button
            type="button"
            aria-label="下一张"
            disabled={props.index === props.items.length - 1}
            onClick={() => props.onJump(props.index + 1)}
          >
            ›
          </button>
          <button
            type="button"
            onClick={() => open("settings")}
            aria-haspopup="dialog"
          >
            设置
          </button>
        </Show>
        <button
          type="button"
          aria-pressed={props.immersive}
          onClick={() => props.onImmersive(!props.immersive)}
        >
          {props.immersive ? "显示工具" : "沉浸"}
        </button>
      </nav>
      <dialog
        ref={dialog}
        class="reader-dialog"
        aria-labelledby="reader-panel-title"
        onClose={() => opener?.focus({ preventScroll: true })}
        onClick={(e) => {
          if (e.target === dialog) {
            const r = dialog.getBoundingClientRect()
            if (
              e.clientX < r.left ||
              e.clientX > r.right ||
              e.clientY < r.top ||
              e.clientY > r.bottom
            )
              close()
          }
        }}
      >
        <header class="reader-panel-header">
          <div>
            <small class="reader-muted">
              {panel() === "pages" ? "页面导航" : "阅读偏好"}
            </small>
            <h2 id="reader-panel-title">
              {panel() === "pages" ? "缩略图" : "阅读设置"}
            </h2>
          </div>
          <button type="button" autofocus aria-label="关闭面板" onClick={close}>
            ×
          </button>
        </header>
        <Show
          when={panel() === "settings"}
          fallback={
            <>
              <form
                class="reader-jump reader-panel-jump"
                onSubmit={(e) => {
                  e.preventDefault()
                  const n = Number(jumpValue())
                  if (Number.isInteger(n) && n >= 1 && n <= props.items.length)
                    navigate(n - 1)
                }}
              >
                <label for="reader-page-number">跳到</label>
                <input
                  id="reader-page-number"
                  type="number"
                  min="1"
                  max={props.items.length}
                  value={jumpValue()}
                  onInput={(e) => setJumpValue(e.currentTarget.value)}
                  required
                  aria-label="跳到第几张"
                />
                <span>/ {props.items.length}</span>
                <button type="submit">跳转</button>
              </form>
              <div class="reader-thumbnails">
                <For each={thumbnails()}>
                  {(image, i) => (
                    <button
                      type="button"
                      class="reader-thumbnail"
                      aria-label={`第 ${page() * pageSize + i() + 1} 张：${image.name}`}
                      aria-current={
                        page() * pageSize + i() === props.index
                          ? "page"
                          : undefined
                      }
                      onClick={() => navigate(page() * pageSize + i())}
                    >
                      <DerivativeThumbnail
                        obj={{ name: image.name, size: image.size, thumb: image.thumb || "", derivatives: image.derivatives }}
                        refresh={props.refreshThumbnail}
                        alt=""
                        loading="lazy"
                        decoding="async"
                        fallbackErr={<span aria-hidden="true">▧</span>}
                      />
                      <span>{page() * pageSize + i() + 1}</span>
                      <small title={image.name}>{image.name}</small>
                    </button>
                  )}
                </For>
              </div>
              <Show when={props.items.length > pageSize}>
                <nav
                  class="reader-thumbnail-pagination"
                  aria-label="缩略图分页"
                >
                  <button
                    type="button"
                    disabled={page() === 0}
                    onClick={() => setPage(page() - 1)}
                  >
                    上一组
                  </button>
                  <span>
                    {page() + 1} / {Math.ceil(props.items.length / pageSize)}
                  </span>
                  <button
                    type="button"
                    disabled={(page() + 1) * pageSize >= props.items.length}
                    onClick={() => setPage(page() + 1)}
                  >
                    下一组
                  </button>
                </nav>
              </Show>
            </>
          }
        >
          <fieldset>
            <legend>{t("derivatives.quality")}</legend>
            <div class="reader-segments">
              <button type="button" aria-pressed={props.quality === "preview"} onClick={() => props.onQuality("preview")}>{t("derivatives.preview")}</button>
              <button type="button" aria-pressed={props.quality === "original"} onClick={() => props.onQuality("original")}>{t("derivatives.original")}</button>
            </div>
            <p class="reader-muted">{t("derivatives.quality_hint")}</p>
          </fieldset>
          <fieldset>
            <legend>阅读方式</legend>
            <div class="reader-segments">
              <button
                type="button"
                aria-pressed={props.mode === "continuous"}
                onClick={() => props.onMode("continuous")}
              >
                纵向连读
              </button>
              <button
                type="button"
                aria-pressed={props.mode === "single"}
                onClick={() => props.onMode("single")}
              >
                单张翻页
              </button>
            </div>
            <p class="reader-muted">
              {props.mode === "continuous"
                ? "向下滚动，双击图片可单张查看。"
                : "左右滑动翻页，双击图片可放大。"}
            </p>
          </fieldset>
          <Show when={props.mode === "continuous"}>
            <fieldset>
              <legend>画面布局</legend>
              <label class="reader-setting-row" for="reader-width">
                阅读宽度
                <select
                  id="reader-width"
                  value={props.width}
                  onChange={(e) => props.onWidth(e.currentTarget.value)}
                >
                  <option value="comfortable">舒适</option>
                  <option value="wide">宽幅</option>
                  <option value="full">铺满</option>
                </select>
              </label>
              <label class="reader-setting-row">
                <span>图片间留白</span>
                <input
                  type="checkbox"
                  checked={props.gap === 4}
                  onChange={(e) => props.onGap(e.currentTarget.checked ? 4 : 0)}
                />
              </label>
            </fieldset>
          </Show>
          <fieldset>
            <legend>专注阅读</legend>
            <label class="reader-setting-row">
              <span>隐藏工具栏</span>
              <input
                type="checkbox"
                checked={props.immersive}
                onChange={(e) => props.onImmersive(e.currentTarget.checked)}
              />
            </label>
            <button type="button" onClick={props.onFullscreen}>
              切换全屏
            </button>
          </fieldset>
          <details class="reader-shortcuts">
            <summary>键盘快捷键</summary>
            <dl>
              <div>
                <dt>上一张 / 下一张</dt>
                <dd>← / →</dd>
              </div>
              <div>
                <dt>第一张 / 最后一张</dt>
                <dd>Home / End</dd>
              </div>
              <div>
                <dt>缩略图 / 设置</dt>
                <dd>G / S</dd>
              </div>
              <div>
                <dt>沉浸 / 全屏</dt>
                <dd>H / F</dd>
              </div>
              <div>
                <dt>关闭面板 / 退出沉浸</dt>
                <dd>Esc</dd>
              </div>
            </dl>
          </details>
        </Show>
      </dialog>
    </>
  )
}
