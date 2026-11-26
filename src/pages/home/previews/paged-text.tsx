import { createEffect, createMemo, createSignal, onCleanup, onMount, Show } from "solid-js"
import { EncodingSelect, MaybeLoading } from "~/components"
import { useFetchText, useRouter, useT } from "~/hooks"
import { decodeText } from "~/utils"
import { uxText } from "~/utils/ux"
import { textPageOffsets } from "./text-pages"
import "./paged-text.css"

const TEXT_FONT_SIZE_KEY = "openlist-paged-text-font-size"
const TEXT_PAGE_KEY_PREFIX = "openlist-paged-text-page:"

const PagedText = () => {
  const t = useT()
  const { pathname } = useRouter()
  const [content] = useFetchText()
  const [encoding, setEncoding] = createSignal("utf-8")
  const [fontSize, setFontSize] = createSignal<number>((() => {
    try {
      const saved = Number(localStorage.getItem(TEXT_FONT_SIZE_KEY))
      return Number.isFinite(saved) && saved >= 13 && saved <= 24 ? saved : 16
    } catch {
      return 16
    }
  })())
  const pageKey = () => `${TEXT_PAGE_KEY_PREFIX}${pathname()}`
  const [page, setPage] = createSignal<number>((() => {
    try {
      const saved = Number(localStorage.getItem(`${TEXT_PAGE_KEY_PREFIX}${pathname()}`))
      return Number.isInteger(saved) && saved >= 0 ? saved : 0
    } catch {
      return 0
    }
  })())
  let root!: HTMLDivElement
  const raw = () => content()?.content
  const text = createMemo(() => {
    const value = raw()
    return typeof value === "string" ? value : value ? decodeText(value, encoding()) : ""
  })
  const offsets = createMemo(() => textPageOffsets(text()))
  const total = () => offsets().length - 1
  const current = () => Math.max(0, Math.min(page(), Math.max(0, total() - 1)))
  const pageText = () => text().slice(offsets()[current()], offsets()[current() + 1])
  // Detection needs only a bounded sample, not another scan of the whole novel.
  const sample = createMemo(() => {
    const value = raw()
    return typeof value === "string" ? undefined : value?.slice(0, 64 * 1024)
  })
  const adjustFontSize = (delta: number) => {
    const next = Math.max(13, Math.min(24, fontSize() + delta))
    setFontSize(next)
    try {
      localStorage.setItem(TEXT_FONT_SIZE_KEY, String(next))
    } catch {
      // Optional local preference
    }
  }
  const navigate = (next: number) => {
    if (!Number.isInteger(next) || next < 0 || next >= total()) return
    setPage(next)
    try {
      localStorage.setItem(pageKey(), String(next))
    } catch {
      // Optional local preference
    }
    root.scrollIntoView({ block: "start" })
  }
  createEffect(() => {
    const key = pageKey()
    try {
      const saved = Number(localStorage.getItem(key))
      setPage(Number.isInteger(saved) && saved >= 0 ? saved : 0)
    } catch {
      setPage(0)
    }
  })
  onMount(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.defaultPrevented || event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return
      const target = event.target as HTMLElement | null
      if (target?.isContentEditable || (target && /INPUT|SELECT|TEXTAREA/.test(target.tagName)) || document.querySelector('[role="dialog"]')) return
      if (event.key === "ArrowLeft" && current() > 0) {
        event.preventDefault()
        navigate(current() - 1)
      } else if (event.key === "ArrowRight" && current() < total() - 1) {
        event.preventDefault()
        navigate(current() + 1)
      }
    }
    document.addEventListener("keydown", onKeyDown)
    onCleanup(() => document.removeEventListener("keydown", onKeyDown))
  })
  const controls = () => (
    <nav class="text-page-controls" aria-label={t("home.preview.paged_text.pagination")}>
      <button type="button" aria-label={t("home.preview.paged_text.previous")} disabled={current() === 0} onClick={() => navigate(current() - 1)}>‹</button>
      <label><input aria-label={t("home.preview.paged_text.page")} type="number" min="1" max={total()} value={current() + 1} onChange={e => {
        navigate(Number(e.currentTarget.value) - 1)
        e.currentTarget.value = String(current() + 1)
      }} /> / {total()}</label>
      <button type="button" aria-label={t("home.preview.paged_text.next")} disabled={current() === total() - 1} onClick={() => navigate(current() + 1)}>›</button>
    </nav>
  )
  return (
    <MaybeLoading loading={content.loading}>
      <div class="paged-text" ref={root}>
        <div class="text-page-heading">
          {controls()}
          <div class="text-page-tools">
            <div class="text-page-font" role="group" aria-label={uxText("字号调节", "Font size", "字體大小")}>
              <button type="button" aria-label={uxText("缩小字号", "Decrease font size", "縮小字體")} disabled={fontSize() <= 13} onClick={() => adjustFontSize(-1)}>A-</button>
              <span>{fontSize()}</span>
              <button type="button" aria-label={uxText("增大字号", "Increase font size", "增大字體")} disabled={fontSize() >= 24} onClick={() => adjustFontSize(1)}>A+</button>
            </div>
            <Show when={sample()}><div class="text-page-encoding"><EncodingSelect inline encoding={encoding()} setEncoding={value => { setPage(0); setEncoding(value) }} referenceText={sample()} /></div></Show>
          </div>
        </div>
        <pre class="text-page-content" style={{ "font-size": `${fontSize()}px` }}>{pageText()}</pre>
        {controls()}
      </div>
    </MaybeLoading>
  )
}

export default PagedText
