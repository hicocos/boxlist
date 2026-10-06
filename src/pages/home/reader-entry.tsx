import { Show } from "solid-js"
import { objStore } from "~/store"
import { ObjType } from "~/types"
import { useRouter } from "~/hooks"
import { bus, joinBase } from "~/utils"
import { uxText } from "~/utils/ux"
import { isReaderImage } from "../reader/reader-utils"
import "./reader-entry.css"

export const GalleryEntry = () => {
  const first = () =>
    objStore.objs.find((obj) => !obj.is_dir && obj.type === ObjType.IMAGE)
  return (
    <Show when={first()}>
      <button
        type="button"
        class="directory-gallery-entry"
        aria-label={uxText("打开目录图片查看器", "Open directory gallery")}
        title={uxText("打开目录图片查看器", "Open directory gallery")}
        onClick={(event) => {
          event.preventDefault()
          event.stopPropagation()
          const image = first()
          if (image) bus.emit("gallery", image.name)
        }}
      >
        <svg
          width="21"
          height="21"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <rect x="3" y="3" width="18" height="18" rx="2" />
          <circle cx="8" cy="8" r="1.5" />
          <path d="m21 15-5-5L5 21" />
        </svg>
      </button>
    </Show>
  )
}

export const ReaderEntry = () => {
  const { pathname, isShare, to } = useRouter()
  const href = () => `/@reader?dir=${encodeURIComponent(pathname())}`
  return (
    <Show when={!isShare() && objStore.objs.some(isReaderImage)}>
      <a
        class="directory-reader-entry"
        href={joinBase(href())}
        aria-label={uxText("阅读目录图片", "Read directory images")}
        title={uxText(
          "阅读目录图片（漫画连读 / 单张）",
          "Read directory images (continuous / single)",
        )}
        onClick={(event) => {
          if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey)
            return
          event.preventDefault()
          event.stopPropagation()
          to(href())
        }}
      >
        <svg
          width="21"
          height="21"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.7"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path d="M12 5v15M12 5C9 3 5 3 2 4v15c3-1 7-1 10 1 3-2 7-2 10-1V4c-3-1-7-1-10 1Z" />
          <path d="m4 14 2-3 3 4M15 9h4M15 13h4" />
        </svg>
      </a>
    </Show>
  )
}
