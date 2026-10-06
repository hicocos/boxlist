import { HStack, VStack, Text } from "@hope-ui/solid"
import {
  batch,
  createEffect,
  createSignal,
  For,
  Show,
  onMount,
  untrack,
} from "solid-js"
import { useT, useRouter } from "~/hooks"
import {
  allChecked,
  checkboxOpen,
  countMsg,
  isIndeterminate,
  local,
  objStore,
  selectAll,
  selectedMsg,
  sortObjs,
} from "~/store"
import { OrderBy } from "~/store"
import { Col, cols, ListItem } from "./ListItem"
import { ItemCheckbox, useSelectWithMouse } from "./helper"
import { bus } from "~/utils"
import { GalleryEntry, ReaderEntry } from "../reader-entry"
import { uxText } from "~/utils/ux"
import { parseFileSortState } from "~/utils/file-sort"
import "./file-rows.css"

export interface SortState {
  orderBy: string
  reverse: boolean
}

const SORT_KEY_PREFIX = "dir_sort_"

export function saveSortState(dir: string, state: SortState) {
  try {
    localStorage.setItem(`${SORT_KEY_PREFIX}${dir}`, JSON.stringify(state))
  } catch (err) {
    console.warn("failed to save sort config:", err)
  }
}

export function loadSortState(dir: string): SortState | null {
  try {
    const item = localStorage.getItem(`${SORT_KEY_PREFIX}${dir}`)
    if (!item) return null
    return parseFileSortState(item)
  } catch (err) {
    console.warn("failed to read sort config:", err)
    return null
  }
}

export const ListTitle = (props: {
  sortCallback: (orderBy: OrderBy, reverse?: boolean) => void
  disableCheckbox?: boolean
  initialOrder?: OrderBy
  initialReverse?: boolean
  readerEntry?: boolean
}) => {
  const t = useT()
  const { pathname } = useRouter()

  const [orderBy, setOrderBy] = createSignal<OrderBy | undefined>(
    props.initialOrder,
  )
  const [reverse, setReverse] = createSignal(props.initialReverse ?? false)

  createEffect(() => {
    if (props.initialOrder !== undefined) {
      setOrderBy(props.initialOrder)
      setReverse(props.initialReverse ?? false)
    }
  })

  createEffect(() => {
    if (orderBy()) {
      saveSortState(pathname(), { orderBy: orderBy()!, reverse: reverse() })
      untrack(() => props.sortCallback(orderBy()!, reverse()))
    }
  })

  const sortButton = (col: Col, label: () => string) => (
    <button
      type="button"
      class="ux-sort-button"
      aria-label={`${t(`home.obj.${col.name}`)}: ${
        orderBy() === col.name
          ? reverse()
            ? uxText("当前降序", "currently descending", "目前降序")
            : uxText("当前升序", "currently ascending", "目前升序")
          : uxText("未排序", "not sorted", "未排序")
      }；${
        orderBy() === col.name && !reverse()
          ? uxText("切换为降序", "sort descending", "切換為降序")
          : uxText("切换为升序", "sort ascending", "切換為升序")
      }`}
      onClick={() =>
        batch(() => {
          if (col.name === orderBy()) setReverse(!reverse())
          else {
            setOrderBy(col.name)
            setReverse(false)
          }
        })
      }
    >
      <span class="ux-sort-label">{label()}</span>
      <span
        class="ux-sort-arrow"
        aria-hidden="true"
        style={{ visibility: orderBy() === col.name ? "visible" : "hidden" }}
      >
        {reverse() ? "↓" : "↑"}
      </span>
    </button>
  )
  return (
    <HStack class="title" w="$full" p="$2">
      <HStack
        w={{ "@initial": "auto", "@md": "50%" }}
        flex={{ "@initial": "1 1 0", "@md": "initial" }}
        spacing="$1"
        minW="0"
      >
        <Show when={!props.disableCheckbox && checkboxOpen()}>
          <ItemCheckbox
            aria-label={uxText(
              "选择全部可见的已加载项目",
              "Select all visible loaded items",
              "選取全部可見的已載入項目",
            )}
            checked={allChecked()}
            indeterminate={isIndeterminate()}
            onChange={(e: any) => {
              selectAll(e.target.checked as boolean)
            }}
          />
        </Show>
        {sortButton(
          cols[0],
          () => selectedMsg() || t(`home.obj.${cols[0].name}`),
        )}
        <Show when={props.readerEntry}>
          <ReaderEntry />
        </Show>
      </HStack>
      <HStack
        w={{ "@initial": "auto", "@md": "17%" }}
        minW="max-content"
        spacing="$1"
        justifyContent="flex-end"
        flexShrink={0}
      >
        <Show when={props.readerEntry}>
          <GalleryEntry />
        </Show>
        {sortButton(cols[1], () => t(`home.obj.${cols[1].name}`))}
      </HStack>
      <HStack
        w={cols[2].w}
        justifyContent="flex-end"
        display={{ "@initial": "none", "@md": "flex" }}
      >
        {sortButton(cols[2], () => t(`home.obj.${cols[2].name}`))}
      </HStack>
    </HStack>
  )
}

const ListLayout = () => {
  const { pathname } = useRouter()

  const [initialOrder, setInitialOrder] = createSignal<OrderBy>()
  const [initialReverse, setInitialReverse] = createSignal(false)

  const { registerSelectContainer, captureContentMenu } = useSelectWithMouse()
  registerSelectContainer()

  onMount(() => {
    const saved = loadSortState(pathname())
    if (saved) {
      setInitialOrder(saved.orderBy as OrderBy)
      setInitialReverse(saved.reverse)
      sortObjs(saved.orderBy as OrderBy, saved.reverse)
    }
  })

  createEffect(() => {
    const length = objStore.objs.length
    untrack(() => {
      const saved = loadSortState(pathname())
      if (length && saved) sortObjs(saved.orderBy as OrderBy, saved.reverse)
    })
  })

  const onDragOver = (e: DragEvent) => {
    const items = Array.from(e.dataTransfer?.items ?? [])
    for (let i = 0; i < items.length; i++) {
      const item = items[i]
      if (item.kind === "file") {
        bus.emit("tool", "upload")
        e.preventDefault()
        break
      }
    }
  }

  return (
    <VStack
      onDragOver={onDragOver}
      oncapture:contextmenu={captureContentMenu}
      class="list viselect-container"
      w="$full"
      spacing="$1"
    >
      <ListTitle
        sortCallback={sortObjs}
        initialOrder={initialOrder()}
        initialReverse={initialReverse()}
        readerEntry
      />
      <For each={objStore.objs}>
        {(obj, i) => {
          return <ListItem obj={obj} index={i()} />
        }}
      </For>
      <Show when={local["show_count_msg"] === "visible"}>
        <Text size="sm" color="$neutral11">
          {countMsg()}
        </Text>
      </Show>
    </VStack>
  )
}

export default ListLayout
