import { Center, VStack, Icon } from "@hope-ui/solid"
import { Motion } from "solid-motionone"
import { useContextMenu } from "solid-contextmenu"
import { batch, Show } from "solid-js"
import { CenterLoading, ImageWithError } from "~/components"
import { useLink, usePath } from "~/hooks"
import { checkboxOpen, getMainColor, selectObj } from "~/store"
import { ObjType, StoreObj } from "~/types"
import { bus } from "~/utils"
import { getIconByObj } from "~/utils/icon"
import { ItemCheckbox, useSelectWithMouse, useFolderVisibility } from "./helper"
import { useReducedMotion } from "~/hooks/useReducedMotion"
import { FileName } from "./FileName"
import { uxText } from "~/utils/ux"

export const ImageItem = (props: { obj: StoreObj; index: number }) => {
  const isVisible = useFolderVisibility()
  const reduced = useReducedMotion()
  const { setPathAs } = usePath()
  const objIcon = (
    <Icon color={getMainColor()} boxSize="$12" as={getIconByObj(props.obj)} />
  )
  const { show } = useContextMenu({ id: 1 })
  const { rawLink } = useLink()
  const {
    openWithDoubleClick,
    toggleWithClick,
    restoreSelectionCache,
    handleItemClick,
  } = useSelectWithMouse()
  return (
    <Show
      when={
        isVisible(props.obj) &&
        !props.obj.is_dir &&
        props.obj.type === ObjType.IMAGE
      }
    >
      <Motion.div
        class="ux-file-row"
        initial={{ opacity: reduced() ? 1 : 0, scale: reduced() ? 1 : 0.9 }}
        animate={{ opacity: 1, scale: 1 }}
        transition={{ duration: reduced() ? 0 : 0.2 }}
        style={{
          "flex-grow": 1,
        }}
      >
        <VStack
          w="$full"
          classList={{ selected: !!props.obj.selected }}
          class="image-item viselect-item"
          data-index={props.index}
          data-name={props.obj.name}
          p="$1"
          spacing="$1"
          rounded="$lg"
          transition="all 0.3s"
          border="2px solid transparent"
          _hover={{
            border: `2px solid ${getMainColor()}`,
          }}
          role="button"
          tabIndex={0}
          aria-label={`${uxText("打开图片", "Open image", "開啟圖片")}: ${props.obj.name}`}
          onKeyDown={(e) => {
            if (e.target !== e.currentTarget) return
            if (e.key === "Enter" || e.key === " ") {
              e.preventDefault()
              bus.emit("gallery", props.obj.name)
            }
          }}
          cursor={
            openWithDoubleClick() || toggleWithClick() ? "default" : "pointer"
          }
          onMouseEnter={() => {
            setPathAs(props.obj.name, props.obj.is_dir, true)
          }}
          onContextMenu={(e: MouseEvent) => {
            batch(() => {
              selectObj(props.obj, true, true)
            })
            show(e, { props: props.obj })
          }}
        >
          <Center w="$full" pos="relative">
            <Show when={checkboxOpen()}>
              <ItemCheckbox
                pos="absolute"
                left="$1"
                top="$1"
                on:mousedown={(e: MouseEvent) => {
                  e.stopPropagation()
                }}
                on:click={(e: MouseEvent) => {
                  e.stopPropagation()
                }}
                aria-label={`${uxText("选择", "Select", "選取")}: ${props.obj.name}`}
                checked={!!props.obj.selected}
                onChange={(e: any) => {
                  selectObj(props.obj, e.target.checked)
                }}
              />
            </Show>
            <ImageWithError
              h="150px"
              w="$full"
              objectFit="cover"
              rounded="$lg"
              shadow="$md"
              fallback={<CenterLoading size="lg" />}
              fallbackErr={objIcon}
              src={rawLink(props.obj)}
              loading="lazy"
              on:dblclick={() => {
                if (!openWithDoubleClick()) return
                bus.emit("gallery", props.obj.name)
                selectObj(props.obj, true, true)
              }}
              on:click={(e: MouseEvent) => {
                handleItemClick(e, props.obj)
                if (
                  e.defaultPrevented ||
                  e.ctrlKey ||
                  e.metaKey ||
                  e.shiftKey ||
                  e.altKey
                )
                  return
                bus.emit("gallery", props.obj.name)
              }}
            />
          </Center>
          <FileName name={props.obj.name} />
        </VStack>
      </Motion.div>
    </Show>
  )
}
