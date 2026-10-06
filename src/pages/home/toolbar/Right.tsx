import {
  Box,
  createDisclosure,
  VStack,
  useColorMode,
  useColorModeValue,
} from "@hope-ui/solid"
import { createMemo, createUniqueId, Show, onCleanup, onMount } from "solid-js"
import { useReducedMotion } from "~/hooks/useReducedMotion"
import { uxText } from "~/utils/ux"
import { RightIcon } from "./Icon"
import { CgMoreO } from "solid-icons/cg"
import { FiSun, FiMoon } from "solid-icons/fi"
import { TbCheckbox } from "solid-icons/tb"
import {
  checkboxOpen,
  me,
  objStore,
  selectAll,
  State,
  toggleCheckbox,
  userCan,
} from "~/store"
import { UserMethods } from "~/types"
import { bus } from "~/utils"
import { operations } from "./operations"
import { IoMagnetOutline } from "solid-icons/io"
import { AiOutlineCloudUpload, AiOutlineSetting } from "solid-icons/ai"
import { RiSystemRefreshLine } from "solid-icons/ri"
import { usePath, useRouter } from "~/hooks"
import { Motion } from "solid-motionone"
import { isTocVisible, setTocDisabled } from "~/components"
import { BiSolidBookContent } from "solid-icons/bi"

export const Right = () => {
  const reducedMotion = useReducedMotion()
  const actionsId = `toolbar-actions-${createUniqueId()}`
  let toggleButton: HTMLButtonElement | undefined
  const { toggleColorMode } = useColorMode()
  const themeIcon = useColorModeValue(FiMoon, FiSun)
  const { isOpen, onToggle, onClose } = createDisclosure({
    defaultIsOpen: localStorage.getItem("more-open") === "true",
    onClose: () => localStorage.setItem("more-open", "false"),
    onOpen: () => localStorage.setItem("more-open", "true"),
  })
  const toggle = () => {
    onToggle()
    queueMicrotask(() => toggleButton?.focus({ preventScroll: true }))
  }
  const isFolder = createMemo(() => objStore.state === State.Folder)
  const { refresh } = usePath()
  const { isShare } = useRouter()
  let toolbarBox: HTMLDivElement | undefined
  onMount(() => {
    const outside = (event: PointerEvent) => {
      if (
        isOpen() &&
        event.target instanceof Node &&
        !toolbarBox?.contains(event.target)
      )
        onClose()
    }
    document.addEventListener("pointerdown", outside, true)
    onCleanup(() => document.removeEventListener("pointerdown", outside, true))
  })
  return (
    <Box
      ref={toolbarBox}
      class="left-toolbar-box"
      pos="fixed"
      zIndex="calc($modal - 1)"
    >
      <Show
        when={isOpen()}
        fallback={
          <RightIcon
            class="toolbar-toggle"
            as={CgMoreO}
            tips="more"
            aria-label={uxText("展开工具栏", "Expand toolbar", "展開工具列")}
            aria-expanded={false}
            aria-controls={actionsId}
            ref={(element: HTMLButtonElement) => {
              toggleButton = element
            }}
            onClick={toggle}
          />
        }
      >
        <VStack
          class="left-toolbar"
          p="$1"
          rounded="$lg"
          spacing="$1"
          // shadow="0px 10px 30px -5px rgba(0, 0, 0, 0.3)"
          // bgColor={useColorModeValue("white", "$neutral4")()}
          bgColor="$neutral1"
          as={Motion.div}
          initial={
            reducedMotion()
              ? { opacity: 1, scale: 1 }
              : { opacity: 0, scale: 0.9 }
          }
          animate={{ opacity: 1, scale: 1 }}
          exit={
            reducedMotion()
              ? { opacity: 1, scale: 1 }
              : { opacity: 0, scale: 0.6 }
          }
          // @ts-ignore
          transition={{ duration: reducedMotion() ? 0 : 0.2 }}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault()
              toggle()
            }
          }}
        >
          <VStack
            spacing="$1"
            class="left-toolbar-in"
            id={actionsId}
            role="group"
            aria-label={uxText("文件工具", "File tools", "檔案工具")}
          >
            <Show when={isFolder() && !isShare()}>
              <RightIcon
                class="toolbar-refresh"
                as={RiSystemRefreshLine}
                tips="refresh"
                onClick={() => {
                  // Read-only visitors may re-fetch; force-refresh remains gated.
                  refresh(
                    undefined,
                    !!(
                      (userCan("write_content") ||
                        objStore.write_content_bypass) &&
                      objStore.write
                    ),
                  )
                }}
              />
            </Show>
            <Show
              when={
                isFolder() &&
                !isShare() &&
                (userCan("write_content") || objStore.write_content_bypass) &&
                objStore.write
              }
            >
              <RightIcon
                as={operations.new_file.icon}
                tips="new_file"
                onClick={() => {
                  bus.emit("tool", "new_file")
                }}
              />
              <RightIcon
                as={operations.mkdir.icon}
                p="$1_5"
                tips="mkdir"
                onClick={() => {
                  bus.emit("tool", "mkdir")
                }}
              />
            </Show>
            <Show
              when={
                isFolder() && !isShare() && userCan("move") && objStore.write
              }
            >
              <RightIcon
                as={operations.recursive_move.icon}
                tips="recursive_move"
                onClick={() => {
                  bus.emit("tool", "recursiveMove")
                }}
              />
            </Show>
            <Show
              when={
                isFolder() && !isShare() && userCan("delete") && objStore.write
              }
            >
              <RightIcon
                as={operations.remove_empty_directory.icon}
                tips="remove_empty_directory"
                onClick={() => {
                  bus.emit("tool", "removeEmptyDirectory")
                }}
              />
            </Show>
            <Show
              when={
                isFolder() && !isShare() && userCan("rename") && objStore.write
              }
            >
              <RightIcon
                as={operations.batch_rename.icon}
                tips="batch_rename"
                onClick={() => {
                  selectAll(true)
                  bus.emit("tool", "batchRename")
                }}
              />
            </Show>
            <Show
              when={
                isFolder() &&
                !isShare() &&
                (userCan("write_content") || objStore.write_content_bypass) &&
                objStore.write
              }
            >
              <RightIcon
                as={AiOutlineCloudUpload}
                tips="upload"
                onClick={() => {
                  bus.emit("tool", "upload")
                }}
              />
            </Show>
            <Show
              when={
                isFolder() &&
                !isShare() &&
                userCan("offline_download") &&
                objStore.write
              }
            >
              <RightIcon
                as={IoMagnetOutline}
                pl="0"
                tips="offline_download"
                onClick={() => {
                  bus.emit("tool", "offline_download")
                }}
              />
            </Show>
            <Show when={isTocVisible()}>
              <RightIcon
                as={BiSolidBookContent}
                tips="toggle_markdown_toc"
                onClick={() => {
                  setTocDisabled((disabled) => !disabled)
                }}
              />
            </Show>
            <Show when={UserMethods.is_admin(me())}>
              <RightIcon
                class="toolbar-checkbox-toggle"
                tips="toggle_checkbox"
                as={TbCheckbox}
                aria-pressed={checkboxOpen()}
                onClick={toggleCheckbox}
              />
            </Show>
            <RightIcon
              class="toolbar-theme-toggle"
              tips="toggle_theme"
              as={themeIcon()}
              onClick={toggleColorMode}
            />
            <RightIcon
              as={AiOutlineSetting}
              class="toolbar-local-settings"
              tips="local_settings"
              aria-haspopup="dialog"
              aria-controls="local-settings-dialog"
              onClick={(
                event: MouseEvent & { currentTarget: HTMLButtonElement },
              ) => {
                event.currentTarget.focus({ preventScroll: true })
                bus.emit("tool", "local_settings")
              }}
            />
          </VStack>
          <RightIcon
            class="toolbar-collapse"
            tips="more"
            as={CgMoreO}
            aria-label={uxText("收起工具栏", "Collapse toolbar", "收合工具列")}
            aria-expanded={true}
            aria-controls={actionsId}
            ref={(element: HTMLButtonElement) => {
              toggleButton = element
            }}
            onClick={toggle}
          />
        </VStack>
      </Show>
    </Box>
  )
}
