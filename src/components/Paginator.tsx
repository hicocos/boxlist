import {
  Box,
  Button,
  HStack,
  IconButton,
  Select,
  SelectContent,
  SelectListbox,
  SelectOption,
  SelectOptionText,
  SelectTrigger,
} from "@hope-ui/solid"
import { createMemo, For, mergeProps, Show } from "solid-js"
import { createStore } from "solid-js/store"
import { FaSolidAngleLeft, FaSolidAngleRight } from "solid-icons/fa"
import { TbSelector } from "solid-icons/tb"

export interface PaginatorProps {
  colorScheme?:
    "primary" | "accent" | "neutral" | "success" | "info" | "warning" | "danger"
  // size?: "xs" | "sm" | "lg" | "xl" | "md";
  defaultCurrent?: number
  current?: number
  disabled?: boolean
  onChange?: (current: number) => void
  hideOnSinglePage?: boolean
  total: number
  defaultPageSize?: number
  maxShowPage?: number
  setResetCallback?: (callback: () => void) => void
}
export const Paginator = (props: PaginatorProps) => {
  const merged = mergeProps(
    {
      maxShowPage: 4,
      defaultPageSize: 30,
      defaultCurrent: 1,
      hideOnSinglePage: true,
    },
    props,
  )
  const [store, setStore] = createStore({
    pageSize: merged.defaultPageSize,
    current: merged.defaultCurrent,
  })
  const current = () => merged.current ?? store.current
  merged.setResetCallback?.(() => {
    setStore("current", merged.defaultCurrent)
  })
  const pages = createMemo(() => {
    return Math.ceil(merged.total / store.pageSize)
  })
  const leftPages = createMemo(() => {
    const page = current()
    const min = Math.max(2, page - Math.floor(merged.maxShowPage / 2))
    return Array.from({ length: Math.max(0, page - min) }, (_, i) => min + i)
  })
  const rightPages = createMemo(() => {
    const page = current()
    const max = Math.min(pages() - 1, page + Math.floor(merged.maxShowPage / 2))
    return Array.from(
      { length: Math.max(0, max - page) },
      (_, i) => page + 1 + i,
    )
  })
  const allPages = createMemo(() => {
    return Array.from({ length: pages() }, (_, i) => 1 + i)
  })
  const size = {
    "@initial": "sm",
    "@md": "md",
  } as const
  const onPageChange = (page: number) => {
    if (merged.disabled) return
    if (merged.current === undefined) setStore("current", page)
    merged.onChange?.(page)
  }
  return (
    <Show when={!merged.hideOnSinglePage || pages() > 1}>
      <HStack spacing="$1">
        <Show when={current() !== 1}>
          <Button
            size={size}
            disabled={merged.disabled}
            colorScheme={merged.colorScheme}
            onClick={() => {
              onPageChange(1)
            }}
            px="$3"
          >
            1
          </Button>
          <IconButton
            size={size}
            disabled={merged.disabled}
            icon={<FaSolidAngleLeft />}
            aria-label="Previous"
            colorScheme={merged.colorScheme}
            onClick={() => {
              onPageChange(current() - 1)
            }}
            w="2rem !important"
          />
        </Show>
        <For each={leftPages()}>
          {(page) => (
            <Button
              size={size}
              disabled={merged.disabled}
              colorScheme={merged.colorScheme}
              onClick={() => {
                onPageChange(page)
              }}
              px={page > 10 ? "$2_5" : "$3"}
            >
              {page}
            </Button>
          )}
        </For>
        <Select
          size={size}
          variant="unstyled"
          value={current()}
          disabled={merged.disabled}
          onChange={(page) => {
            onPageChange(+page)
          }}
        >
          <SelectTrigger
            as={Button}
            size={size}
            width="auto"
            px="$1"
            variant="solid"
            colorScheme={merged.colorScheme}
          >
            <Box px={current() > 10 ? "$1_5" : "$2"}>{current()}</Box>
            <TbSelector />
          </SelectTrigger>
          <SelectContent minW="80px">
            <SelectListbox>
              <For each={allPages()}>
                {(page) => (
                  <SelectOption value={page}>
                    <SelectOptionText px="$2">{page}</SelectOptionText>
                  </SelectOption>
                )}
              </For>
            </SelectListbox>
          </SelectContent>
        </Select>
        <For each={rightPages()}>
          {(page) => (
            <Button
              size={size}
              disabled={merged.disabled}
              colorScheme={merged.colorScheme}
              onClick={() => {
                onPageChange(page)
              }}
              px={page > 10 ? "$2_5" : "$3"}
            >
              {page}
            </Button>
          )}
        </For>
        <Show when={current() !== pages()}>
          <IconButton
            size={size}
            disabled={merged.disabled}
            icon={<FaSolidAngleRight />}
            aria-label="Next"
            colorScheme={merged.colorScheme}
            onClick={() => {
              onPageChange(current() + 1)
            }}
            w="2rem !important"
          />
          <Button
            size={size}
            disabled={merged.disabled}
            colorScheme={merged.colorScheme}
            onClick={() => {
              onPageChange(pages())
            }}
            px={pages() > 10 ? "$2_5" : "$3"}
          >
            {pages()}
          </Button>
        </Show>
      </HStack>
    </Show>
  )
}
