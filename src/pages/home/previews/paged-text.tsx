import { createMemo, createSignal, Show } from "solid-js"
import { EncodingSelect, MaybeLoading } from "~/components"
import { useFetchText, useT } from "~/hooks"
import { decodeText } from "~/utils"
import { textPageOffsets } from "./text-pages"
import "./paged-text.css"

const PagedText = () => {
  const t = useT()
  const [content] = useFetchText()
  const [encoding, setEncoding] = createSignal("utf-8")
  const [page, setPage] = createSignal(0)
  let root!: HTMLDivElement
  const raw = () => content()?.content
  const text = createMemo(() => {
    const value = raw()
    return typeof value === "string" ? value : value ? decodeText(value, encoding()) : ""
  })
  const offsets = createMemo(() => textPageOffsets(text()))
  const total = () => offsets().length - 1
  const current = () => Math.min(page(), total() - 1)
  const pageText = () => text().slice(offsets()[current()], offsets()[current() + 1])
  // Detection needs only a bounded sample, not another scan of the whole novel.
  const sample = createMemo(() => {
    const value = raw()
    return typeof value === "string" ? undefined : value?.slice(0, 64 * 1024)
  })
  const navigate = (next: number) => {
    if (!Number.isInteger(next) || next < 0 || next >= total()) return
    setPage(next)
    root.scrollIntoView({ block: "start" })
  }
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
          <Show when={sample()}><div class="text-page-encoding"><EncodingSelect encoding={encoding()} setEncoding={value => { setPage(0); setEncoding(value) }} referenceText={sample()} /></div></Show>
        </div>
        <pre class="text-page-content">{pageText()}</pre>
        {controls()}
      </div>
    </MaybeLoading>
  )
}

export default PagedText
