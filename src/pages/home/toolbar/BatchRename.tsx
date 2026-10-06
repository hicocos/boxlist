import {
  Button,
  createDisclosure,
  HStack,
  Modal,
  ModalBody,
  ModalContent,
  ModalFooter,
  ModalHeader,
  ModalOverlay,
  Text,
  VStack,
  Radio,
  RadioGroup,
  Input,
} from "@hope-ui/solid"
import { createMemo, createSignal, For, onCleanup, Show } from "solid-js"
import { useFetch, usePath, useRouter, useT } from "~/hooks"
import { objStore, selectableObjs, selectedObjs, State, userCan } from "~/store"
import { bus, fsBatchRename, handleRespWithNotifySuccess } from "~/utils"
import { RenameObj, StoreObj } from "~/types"
import { uxText } from "~/utils/ux"
import {
  generateFileRenames,
  renameOptionsError,
  validateFileRenames,
} from "~/utils/file-renames"
import { RenameItem } from "./RenameItem"

export const BatchRename = () => {
  const preview = createDisclosure()
  const editor = createDisclosure()
  const [loading, ok] = useFetch(fsBatchRename)
  const { pathname, isShare } = useRouter()
  const { refresh } = usePath()
  const t = useT()
  const [type, setType] = createSignal("1")
  const [srcName, setSrcName] = createSignal("")
  const [newName, setNewName] = createSignal("")
  const [paddingZeros, setPaddingZeros] = createSignal("")
  const [matchNames, setMatchNames] = createSignal<RenameObj[]>([])
  const [sources, setSources] = createSignal<StoreObj[]>([])
  const [sourceNames, setSourceNames] = createSignal<string[]>([])
  const [directory, setDirectory] = createSignal("")
  const permitted = () =>
    !isShare() &&
    !!objStore.write &&
    userCan("rename") &&
    [State.Folder, State.FetchingMore].includes(objStore.state)
  const contextValid = () =>
    permitted() &&
    pathname() === directory() &&
    sources().length > 0 &&
    sources().every(
      (obj, i) =>
        selectableObjs().includes(obj) && obj.name === sourceNames()[i],
    )
  const options = () => ({
    type: type(),
    source: srcName(),
    replacement: newName(),
    padding: paddingZeros(),
  })
  const optionError = createMemo(() => renameOptionsError(options()))
  const optionMessage = () => {
    switch (optionError()) {
      case "source":
        return uxText(
          "请输入匹配内容或编号模板。",
          "Enter a match or numbering template.",
          "請輸入匹配內容或編號範本。",
        )
      case "regex":
        return t("global.invalid_regex")
      case "number":
        return uxText(
          "起始编号必须是非负安全整数。",
          "The starting number must be a non-negative safe integer.",
          "起始編號必須是非負安全整數。",
        )
      case "padding":
        return uxText(
          "补零位数须为 0–255 的整数。",
          "Zero-padding must be an integer from 0 to 255.",
          "補零位數須為 0–255 的整數。",
        )
      default:
        return ""
    }
  }
  const validation = createMemo(() =>
    validateFileRenames(
      matchNames(),
      objStore.objs.map((obj) => obj.name),
    ),
  )
  const closeEditor = () => {
    if (!loading()) editor.onClose()
  }
  const closePreview = () => {
    if (!loading()) preview.onClose()
  }
  const handler = (name: string) => {
    if (name !== "batchRename" || !permitted() || loading()) return
    const targets = selectedObjs()
    if (!targets.length) return
    setSources([...targets])
    setSourceNames(targets.map((obj) => obj.name))
    setDirectory(pathname())
    setMatchNames([])
    editor.onOpen()
  }
  bus.on("tool", handler)
  onCleanup(() => bus.off("tool", handler))
  const submit = () => {
    if (optionError() || !contextValid() || loading()) return
    setMatchNames(generateFileRenames(sources(), options()))
    editor.onClose()
    preview.onOpen()
  }
  const confirm = async () => {
    // Recheck the real directory, eligibility, permissions and generated payload.
    const result = validation()
    if (loading() || !contextValid() || !result.canSubmit) return
    const resp = await ok(directory(), result.changes)
    handleRespWithNotifySuccess(resp, () => {
      setMatchNames([])
      setSources([])
      setSrcName("")
      setNewName("")
      setPaddingZeros("")
      setType("1")
      refresh()
      editor.onClose()
      preview.onClose()
    })
  }
  const enterPreview = (event: KeyboardEvent) => {
    if (event.key === "Enter" && !event.isComposing) {
      event.preventDefault()
      submit()
    }
  }
  const sourceLabel = () =>
    type() === "2"
      ? uxText("名称模板", "Name template", "名稱範本")
      : uxText("查找内容", "Find", "尋找內容")
  const replacementLabel = () =>
    type() === "2"
      ? uxText("起始编号", "Starting number", "起始編號")
      : uxText(
          "替换为（可留空）",
          "Replace with (may be empty)",
          "取代為（可留空）",
        )
  return (
    <>
      <Modal
        blockScrollOnMount={false}
        opened={editor.isOpen()}
        onClose={closeEditor}
        initialFocus="#batch-rename-source"
        size="md"
      >
        <ModalOverlay />
        <ModalContent maxW="calc(100vw - 24px)">
          <ModalHeader>{t("home.toolbar.batch_rename")}</ModalHeader>
          <ModalBody>
            <Text mb="$2">
              {uxText(
                `本次处理 ${sources().length} 个可见的已加载项目。`,
                `${sources().length} visible loaded items in this batch.`,
                `本次處理 ${sources().length} 個可見的已載入項目。`,
              )}
            </Text>
            <RadioGroup value={type()} onChange={setType}>
              <HStack spacing="$3" flexWrap="wrap">
                <Radio value="1">{t("home.toolbar.regex_rename")}</Radio>
                <Radio value="2">{t("home.toolbar.sequential_renaming")}</Radio>
                <Radio value="3">{t("home.toolbar.find_replace")}</Radio>
              </HStack>
            </RadioGroup>
            <VStack spacing="$2" alignItems="stretch">
              <p style={{ margin: "10px 0", "overflow-wrap": "anywhere" }}>
                {t(
                  type() === "1"
                    ? "home.toolbar.regular_rename"
                    : type() === "2"
                      ? "home.toolbar.sequential_renaming_desc"
                      : "home.toolbar.find_replace_desc",
                )}
              </p>
              <label for="batch-rename-source">{sourceLabel()}</label>
              <Input
                id="batch-rename-source"
                type="text"
                value={srcName()}
                onInput={(e) => setSrcName(e.currentTarget.value)}
                onKeyDown={enterPreview}
                aria-describedby="batch-rename-options-error"
              />
              <label for="batch-rename-replacement">{replacementLabel()}</label>
              <Input
                id="batch-rename-replacement"
                type={type() === "2" ? "number" : "text"}
                min="0"
                step="1"
                value={newName()}
                onInput={(e) => setNewName(e.currentTarget.value)}
                onKeyDown={enterPreview}
                aria-describedby="batch-rename-options-error"
              />
              <Show when={type() === "2"}>
                <label for="batch-rename-padding">
                  {uxText(
                    "补零位数（可选）",
                    "Zero-padding width (optional)",
                    "補零位數（可選）",
                  )}
                </label>
                <Input
                  id="batch-rename-padding"
                  type="number"
                  min="0"
                  max="255"
                  step="1"
                  value={paddingZeros()}
                  onInput={(e) => setPaddingZeros(e.currentTarget.value)}
                  onKeyDown={enterPreview}
                />
              </Show>
              <Text
                id="batch-rename-options-error"
                color="$danger11"
                fontSize="$sm"
                aria-live="polite"
              >
                {optionMessage()}
              </Text>
              <Show when={!contextValid()}>
                <Text color="$danger11">
                  {uxText(
                    "目录、项目或权限已变化，请关闭后重新选择。",
                    "Directory, items or permissions changed. Close and select again.",
                    "目錄、項目或權限已變更，請關閉後重新選取。",
                  )}
                </Text>
              </Show>
            </VStack>
          </ModalBody>
          <ModalFooter gap="$2">
            <Button onClick={closeEditor} colorScheme="neutral">
              {t("global.cancel")}
            </Button>
            <Button
              onClick={submit}
              disabled={!!optionError() || !contextValid()}
            >
              {uxText("预览", "Preview", "預覽")}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
      <Modal size="xl" opened={preview.isOpen()} onClose={closePreview}>
        <ModalOverlay />
        <ModalContent maxW="calc(100vw - 24px)">
          <ModalHeader>{t("home.toolbar.regex_rename_preview")}</ModalHeader>
          <ModalBody>
            <p aria-live="polite">
              {uxText(
                `${validation().changes.length} 项变更；${validation().unchanged} 项名称不变（跳过）。`,
                `${validation().changes.length} changes; ${validation().unchanged} unchanged (skipped).`,
                `${validation().changes.length} 項變更；${validation().unchanged} 項名稱不變（略過）。`,
              )}
            </p>
            <Show when={!matchNames().length}>
              <Text>
                {uxText(
                  "没有匹配的项目。请返回修改规则。",
                  "No matching items. Go back to edit the rule.",
                  "沒有匹配的項目。請返回修改規則。",
                )}
              </Text>
            </Show>
            <Show when={validation().invalid}>
              <Text color="$danger11" role="alert">
                {uxText(
                  "存在无效名称或冲突，请返回修改。",
                  "Invalid names or conflicts found. Go back to edit.",
                  "存在無效名稱或衝突，請返回修改。",
                )}
              </Text>
            </Show>
            <Show when={objStore.total > objStore.objs.length}>
              <Text fontSize="$sm">
                {uxText(
                  "仅处理已加载项目；未加载项目的名称冲突由服务器最终检查。",
                  "Only loaded items are processed; the server makes the final conflict check for unloaded items.",
                  "僅處理已載入項目；未載入項目的名稱衝突由伺服器最終檢查。",
                )}
              </Text>
            </Show>
            <Show when={!contextValid()}>
              <Text color="$danger11" role="alert">
                {uxText(
                  "目录、项目或权限已变化，不能提交。",
                  "Directory, items or permissions changed; submission is blocked.",
                  "目錄、項目或權限已變更，無法提交。",
                )}
              </Text>
            </Show>
            <VStack w="$full" spacing="$1">
              <For each={validation().rows}>
                {(obj, i) => (
                  <RenameItem
                    obj={obj}
                    index={i()}
                    errors={obj.errors}
                    unchanged={obj.unchanged}
                  />
                )}
              </For>
            </VStack>
          </ModalBody>
          <ModalFooter gap="$2" flexWrap="wrap">
            <Button
              onClick={closePreview}
              disabled={loading()}
              colorScheme="neutral"
            >
              {t("global.cancel")}
            </Button>
            <Button
              onClick={() => {
                preview.onClose()
                editor.onOpen()
              }}
              disabled={loading()}
              colorScheme="neutral"
            >
              {t("global.back")}
            </Button>
            <Button
              loading={loading()}
              disabled={!validation().canSubmit || !contextValid() || loading()}
              onClick={confirm}
            >
              {uxText(
                `确认 ${validation().changes.length} 项变更`,
                `Confirm ${validation().changes.length} changes`,
                `確認 ${validation().changes.length} 項變更`,
              )}
            </Button>
          </ModalFooter>
        </ModalContent>
      </Modal>
    </>
  )
}
