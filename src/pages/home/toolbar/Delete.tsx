import {
  Modal,
  ModalOverlay,
  ModalContent,
  ModalHeader,
  ModalBody,
  ModalFooter,
  Button,
  createDisclosure,
  Text,
} from "@hope-ui/solid"
import { createSignal, For, onCleanup, Show } from "solid-js"
import { useFetch, usePath, useRouter, useT } from "~/hooks"
import { objStore, selectableObjs, selectedObjs, State, userCan } from "~/store"
import { StoreObj } from "~/types"
import { bus, fsRemove, handleRespWithNotifySuccess } from "~/utils"
import { uxText } from "~/utils/ux"

export const Delete = () => {
  const t = useT()
  const { isOpen, onOpen, onClose } = createDisclosure()
  const [loading, ok] = useFetch(fsRemove)
  const { refresh } = usePath()
  const { pathname, isShare } = useRouter()
  const [targets, setTargets] = createSignal<StoreObj[]>([])
  const [names, setNames] = createSignal<string[]>([])
  const [directory, setDirectory] = createSignal("")
  const permitted = () =>
    !isShare() &&
    !!objStore.write &&
    userCan("delete") &&
    [State.Folder, State.FetchingMore].includes(objStore.state)
  const valid = () =>
    permitted() &&
    pathname() === directory() &&
    targets().length > 0 &&
    targets().every(
      (obj, i) => selectableObjs().includes(obj) && obj.name === names()[i],
    )
  const close = () => {
    if (!loading()) onClose()
  }
  const handler = (name: string) => {
    if (name !== "delete" || !permitted() || loading()) return
    const selected = selectedObjs()
    if (!selected.length) return
    setTargets([...selected])
    setNames(selected.map((obj) => obj.name))
    setDirectory(pathname())
    onOpen()
  }
  bus.on("tool", handler)
  onCleanup(() => bus.off("tool", handler))
  return (
    <Modal
      blockScrollOnMount={false}
      opened={isOpen()}
      onClose={close}
      initialFocus="#delete-cancel"
      size={{ "@initial": "xs", "@md": "md" }}
    >
      <ModalOverlay />
      <ModalContent maxW="calc(100vw - 24px)">
        <ModalHeader>
          {uxText(
            `删除 ${names().length} 个项目？`,
            `Delete ${names().length} items?`,
            `刪除 ${names().length} 個項目？`,
          )}
        </ModalHeader>
        <ModalBody>
          <p>{t("home.toolbar.delete-tips")}</p>
          <Text my="$2">
            {uxText(
              `${targets().filter((obj) => obj.is_dir).length} 个文件夹，${targets().filter((obj) => !obj.is_dir).length} 个文件`,
              `${targets().filter((obj) => obj.is_dir).length} folders, ${targets().filter((obj) => !obj.is_dir).length} files`,
              `${targets().filter((obj) => obj.is_dir).length} 個資料夾，${targets().filter((obj) => !obj.is_dir).length} 個檔案`,
            )}
          </Text>
          <ul style={{ "padding-left": "1.2em", "overflow-wrap": "anywhere" }}>
            <For each={names().slice(0, 5)}>{(name) => <li>{name}</li>}</For>
          </ul>
          <Show when={names().length > 5}>
            <details>
              <summary style={{ "min-height": "44px", cursor: "pointer" }}>
                {uxText(
                  `查看其余 ${names().length - 5} 项`,
                  `Show ${names().length - 5} more items`,
                  `查看其餘 ${names().length - 5} 項`,
                )}
              </summary>
              <ul
                style={{ "padding-left": "1.2em", "overflow-wrap": "anywhere" }}
              >
                <For each={names().slice(5)}>{(name) => <li>{name}</li>}</For>
              </ul>
            </details>
          </Show>
          <Show when={!valid()}>
            <Text color="$danger11" role="alert">
              {uxText(
                "目录、项目或权限已变化，请重新选择。",
                "Directory, items or permissions changed. Select again.",
                "目錄、項目或權限已變更，請重新選取。",
              )}
            </Text>
          </Show>
        </ModalBody>
        <ModalFooter gap="$2">
          <Button
            id="delete-cancel"
            onClick={close}
            disabled={loading()}
            colorScheme="neutral"
          >
            {t("global.cancel")}
          </Button>
          <Button
            colorScheme="danger"
            loading={loading()}
            disabled={!valid() || loading()}
            onClick={async () => {
              if (!valid() || loading()) return
              const resp = await ok(directory(), [...names()])
              handleRespWithNotifySuccess(resp, () => {
                refresh()
                onClose()
              })
            }}
          >
            {uxText(
              `删除 ${names().length} 项`,
              `Delete ${names().length} items`,
              `刪除 ${names().length} 項`,
            )}
          </Button>
        </ModalFooter>
      </ModalContent>
    </Modal>
  )
}
