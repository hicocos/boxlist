import { Checkbox, hope } from "@hope-ui/solid"
import { createEffect, createMemo, onCleanup, untrack } from "solid-js"
import { useContextMenu } from "solid-contextmenu"
import SelectionArea from "@viselect/vanilla"
import {
  checkboxOpen,
  haveSelected,
  local,
  objStore,
  oneChecked,
  selectAll,
  selectedObjs,
  selectObj,
  registerSelectionScope,
  restoreSelectedObjs,
  layout,
  getHideFiles,
} from "~/store"
import { isMobile } from "~/utils/compatibility"
import { ObjType, StoreObj } from "~/types"
import { useRouter } from "~/hooks"
import { pathJoin } from "~/utils"
import { nativeLinkClick } from "~/utils/file-selection"

let selectedCache: StoreObj[] | null = null

/** Stateful /g and /y hide patterns must not alternate as rows are rendered. */
export function useFolderVisibility() {
  const { pathname } = useRouter()
  return (obj: StoreObj) =>
    !getHideFiles().some((regexp) => {
      regexp.lastIndex = 0
      const hidden = regexp.test(pathJoin(pathname(), obj.name))
      regexp.lastIndex = 0
      return hidden
    })
}

export function useSelectWithMouse() {
  const isMouseSupported = () => !isMobile && checkboxOpen()
  const openWithDoubleClick = () =>
    isMouseSupported() && local["open_item_on_checkbox"] === "dblclick"
  const toggleWithClick = () =>
    isMouseSupported() &&
    local["open_item_on_checkbox"] === "disable_while_checked" &&
    haveSelected()
  const saveSelectionCache = () => {
    selectedCache = selectedObjs()
  }
  const restoreSelectionCache = () => {
    if (!isMouseSupported()) return true
    if (selectedCache === null) return false
    restoreSelectedObjs(selectedCache)
    return true
  }

  const handleItemClick = (event: MouseEvent, obj: StoreObj) => {
    // Keyboard/AT Enter and modified links belong to the native anchor/router.
    if (event.defaultPrevented || nativeLinkClick(event)) return
    if (event.shiftKey && isMouseSupported()) {
      event.preventDefault()
      restoreSelectionCache()
      selectObj(obj, true, false, true)
      return
    }
    if (isMouseSupported() && !restoreSelectionCache()) {
      event.preventDefault() // click emitted at the end of a drag
      return
    }
    if (openWithDoubleClick() || toggleWithClick()) {
      event.preventDefault()
      selectObj(obj, !obj.selected, false, false)
    }
  }

  const registerSelectContainer = () => {
    const isVisible = useFolderVisibility()
    const visible = createMemo(() => {
      const all = objStore.objs.filter(isVisible)
      if (layout() !== "image") return all
      const images = all.filter(
        (obj) => !obj.is_dir && obj.type === ObjType.IMAGE,
      )
      const folders = all.filter((obj) => obj.is_dir)
      if (local["show_folder_in_image_view"] === "top")
        return [...folders, ...images]
      if (local["show_folder_in_image_view"] === "bottom")
        return [...images, ...folders]
      return images
    })
    onCleanup(registerSelectionScope(visible))
    // Discard selections that cease to be visible; never resurrect them later.
    createEffect(() => {
      const allowed = new Set(visible())
      untrack(() =>
        restoreSelectedObjs(
          objStore.objs.filter((obj) => obj.selected && allowed.has(obj)),
        ),
      )
    })
    createEffect(() => {
      const area = document.querySelector(".viselect-container")
      if (!area) return
      area.addEventListener("mousedown", saveSelectionCache)
      onCleanup(() => area.removeEventListener("mousedown", saveSelectionCache))
      if (!isMouseSupported()) return
      const selection = new SelectionArea({
        selectionAreaClass: "viselect-selection-area",
        features: { singleTap: { allow: false } },
        startAreas: [".viselect-container"],
        boundaries: [".viselect-container"],
        selectables: [".viselect-item"],
      })
      const resolve = (el: Element) => {
        const value = el.getAttribute("data-index")
        if (value === null) return undefined
        const index = Number(value)
        const obj = Number.isInteger(index) ? objStore.objs[index] : undefined
        return obj?.name === el.getAttribute("data-name") ? obj : undefined
      }
      selection.on("beforestart", ({ event }) => {
        const ev = event as MouseEvent
        if (
          ev.ctrlKey ||
          ev.metaKey ||
          ev.altKey ||
          ev.shiftKey ||
          ev.button !== 0 ||
          (ev.target as Element)?.closest(
            "button,input,label,summary,[data-selection-ignore]",
          )
        )
          return false
        saveSelectionCache()
        selection.clearSelection(true, true)
        selection.select(".viselect-item.selected", true)
      })
      selection.on("start", ({ event }) => {
        const ev = event as MouseEvent
        if (ev.type === "mousemove" || ev.type === "pointermove")
          selectedCache = null
        if (!ev.shiftKey && !ev.ctrlKey && !ev.metaKey) {
          selectAll(false)
          selection.clearSelection(true)
        }
      })
      selection.on(
        "move",
        ({
          store: {
            changed: { added, removed },
          },
        }) => {
          selectedCache = null
          for (const el of added) {
            const obj = resolve(el)
            if (obj) selectObj(obj, true, false, false)
          }
          for (const el of removed) {
            const obj = resolve(el)
            if (obj) selectObj(obj, false, false, false)
          }
        },
      )
      onCleanup(() => {
        selection.destroy()
        selectedCache = null
      })
    })
  }

  const { show } = useContextMenu({ id: 1 })
  const captureContentMenu = (e: MouseEvent) => {
    if ((e.target as Element).closest("[data-selection-ignore]")) return
    e.preventDefault()
    if (haveSelected() && !oneChecked()) {
      const row = (e.target as Element).closest(".viselect-item")
      const value = row?.getAttribute("data-index")
      if (value == null) return
      const obj = objStore.objs[Number(value)]
      if (!obj?.selected || obj.name !== row?.getAttribute("data-name")) return
      e.stopPropagation()
      show(e, { props: objStore.obj })
    }
  }
  return {
    isMouseSupported,
    openWithDoubleClick,
    toggleWithClick,
    restoreSelectionCache,
    handleItemClick,
    registerSelectContainer,
    captureContentMenu,
  }
}

// A real 44px label target, not an invisible pseudo-element over the filename.
export const ItemCheckbox = hope(Checkbox, {
  baseStyle: {
    minWidth: "44px",
    minHeight: "44px",
    flexShrink: 0,
    justifyContent: "center",
  },
})
