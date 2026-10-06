import { Center, VStack, Icon } from "@hope-ui/solid"
import { Motion } from "solid-motionone"
import { useContextMenu } from "solid-contextmenu"
import { batch, Show } from "solid-js"
import { CenterLoading, LinkWithPush, ImageWithError } from "~/components"
import { usePath, useRouter } from "~/hooks"
import { checkboxOpen, getMainColor, local, selectObj } from "~/store"
import { ObjType, StoreObj } from "~/types"
import { bus, hoverColor } from "~/utils"
import { getIconByObj } from "~/utils/icon"
import { ItemCheckbox, useSelectWithMouse, useFolderVisibility } from "./helper"
import { useReducedMotion } from "~/hooks/useReducedMotion"
import { FileName } from "./FileName"
import { uxText } from "~/utils/ux"

export const GridItem = (props: { obj: StoreObj; index: number }) => {
  const isVisible = useFolderVisibility()
  const reduced = useReducedMotion()
  const { setPathAs } = usePath()
  const objIcon = (
    <Icon
      color={getMainColor()}
      boxSize={`${parseInt(local["grid_item_size"]) - 30}px`}
      as={getIconByObj(props.obj)}
    />
  )
  const { show } = useContextMenu({ id: 1 })
  const { pushHref, to } = useRouter()
  const {
    openWithDoubleClick,
    toggleWithClick,
    restoreSelectionCache,
    handleItemClick,
  } = useSelectWithMouse()
  return (
    <Show when={isVisible(props.obj)}>
      <Motion.div
        class="ux-file-row"
        initial={{ opacity: reduced() ? 1 : 0, scale: reduced() ? 1 : 0.9 }}
        animate={{ opacity: 1, scale: 1 }}
        transition={{ duration: reduced() ? 0 : 0.2 }}
        style={{
          width: "100%",
        }}
      >
        <VStack
          classList={{ selected: !!props.obj.selected }}
          class="grid-item viselect-item"
          data-index={props.index}
          data-name={props.obj.name}
          w="$full"
          p="$1"
          spacing="$1"
          rounded="$lg"
          transition="all 0.3s"
          _hover={{
            transform: "scale(1.06)",
            bgColor: hoverColor(),
          }}
          as={LinkWithPush}
          href={props.obj.name}
          cursor={
            openWithDoubleClick() || toggleWithClick() ? "default" : "pointer"
          }
          bgColor={props.obj.selected ? hoverColor() : undefined}
          on:dblclick={(e: MouseEvent) => {
            if (
              e.ctrlKey ||
              e.metaKey ||
              e.shiftKey ||
              e.altKey ||
              !openWithDoubleClick()
            )
              return
            selectObj(props.obj, true, true)
            to(pushHref(props.obj.name))
          }}
          on:click={(e: MouseEvent) => handleItemClick(e, props.obj)}
          onMouseEnter={() => {
            setPathAs(props.obj.name, props.obj.is_dir, true)
          }}
          onContextMenu={(e: MouseEvent) => {
            batch(() => {
              // if (!checkboxOpen()) {
              //   toggleCheckbox();
              // }
              selectObj(props.obj, true, true)
            })
            show(e, { props: props.obj })
          }}
        >
          <Center
            class="item-thumbnail"
            h={`${parseInt(local["grid_item_size"])}px`}
            w="$full"
            cursor={props.obj.type !== ObjType.IMAGE ? "inherit" : "pointer"}
            on:click={(e: MouseEvent) => {
              if (props.obj.type !== ObjType.IMAGE) return
              if (e.ctrlKey || e.metaKey || e.shiftKey) return
              if (!restoreSelectionCache()) return
              bus.emit("gallery", props.obj.name)
              e.preventDefault()
              e.stopPropagation()
            }}
            pos="relative"
          >
            <Show when={checkboxOpen()}>
              <ItemCheckbox
                pos="absolute"
                left="$1"
                top="$1"
                // colorScheme="neutral"
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
            <Show when={props.obj.thumb} fallback={objIcon}>
              <ImageWithError
                maxH="$full"
                maxW="$full"
                rounded="$lg"
                shadow="$md"
                fallback={<CenterLoading size="lg" />}
                fallbackErr={objIcon}
                src={props.obj.thumb}
                loading="lazy"
              />
            </Show>
          </Center>
          <FileName name={props.obj.name} directory={props.obj.is_dir} />
        </VStack>
      </Motion.div>
    </Show>
  )
}
