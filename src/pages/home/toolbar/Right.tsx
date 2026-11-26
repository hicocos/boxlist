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
import { FiSun, FiMoon, FiImage } from "solid-icons/fi"
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
import { usePath, useRouter, useT } from "~/hooks"
import { Motion } from "solid-motionone"
import { isTocVisible, setTocDisabled } from "~/components"
import { BiSolidBookContent } from "solid-icons/bi"

export const Right = () => {
  const reducedMotion = useReducedMotion()
  const t = useT()
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
  const triggerTool = (tool: string) => {
    bus.emit("tool", tool)
    if (window.innerWidth <= 640) onClose()
  }
  const hasSecondaryTools = createMemo(() => {
    if (!isFolder() || isShare()) return false
    return (
      (objStore.write &&
        (userCan("move") ||
          userCan("delete") ||
          userCan("rename") ||
          userCan("offline_download"))) ||
      UserMethods.is_admin(me())
    )
  })
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
      zIndex="calc($overlay - 1)"
    >
      <Show
        when={isOpen()}
        fallback={
          <RightIcon
            class="toolbar-toggle"
            as={CgMoreO}
            hideTooltip
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
          px="$0_5"
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
            {/* Everyday high-frequency tools first so they stay above the fold on mobile. */}
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
                onClick={() => triggerTool("upload")}
              />
              <RightIcon
                as={operations.mkdir.icon}
                p="$1_5"
                tips="mkdir"
                onClick={() => triggerTool("mkdir")}
              />
              <RightIcon
                as={operations.new_file.icon}
                tips="new_file"
                onClick={() => triggerTool("new_file")}
              />
            </Show>
            <Show when={isFolder() && !isShare() && !UserMethods.is_guest(me())}>
              <RightIcon
                class="toolbar-refresh"
                as={RiSystemRefreshLine}
                tips="refresh"
                onClick={() => {
                  // Force-refresh remains gated by write permissions.
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
            <Show when={UserMethods.is_admin(me())}>
              <RightIcon
                class="toolbar-checkbox-toggle"
                tips="toggle_checkbox"
                as={TbCheckbox}
                aria-pressed={checkboxOpen()}
                onClick={toggleCheckbox}
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
                triggerTool("local_settings")
              }}
            />
            <Show when={hasSecondaryTools()}>
              <div class="toolbar-divider" role="separator" />
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
                onClick={() => triggerTool("offline_download")}
              />
            </Show>
            <Show when={isFolder() && !isShare() && UserMethods.is_admin(me())}>
              <RightIcon
                as={FiImage}
                name="generate_derivatives"
                aria-label={t("derivatives.generate")}
                aria-haspopup="dialog"
                onClick={() => triggerTool("generate_derivatives")}
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
                  triggerTool("batchRename")
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
                onClick={() => triggerTool("recursiveMove")}
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
                onClick={() => triggerTool("removeEmptyDirectory")}
              />
            </Show>
          </VStack>
          <RightIcon
            class="toolbar-collapse"
            hideTooltip
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
