import {
  Button,
  createDisclosure,
  Drawer,
  DrawerBody,
  DrawerCloseButton,
  DrawerContent,
  DrawerHeader,
  DrawerOverlay,
  FormControl,
  FormLabel,
  Input,
  Select,
  SelectContent,
  SelectIcon,
  SelectListbox,
  SelectOption,
  SelectOptionIndicator,
  SelectOptionText,
  SelectPlaceholder,
  SelectTrigger,
  SelectValue,
  Switch as HopeSwitch,
  IconButton,
  useColorMode,
  useColorModeValue,
} from "@hope-ui/solid"
import {
  createEffect,
  createSignal,
  For,
  Match,
  onCleanup,
  Show,
  Switch,
} from "solid-js"
import { FaSolidMinus, FaSolidPlus } from "solid-icons/fa"
import { FiSun, FiMoon } from "solid-icons/fi"
import { IoLanguageOutline } from "solid-icons/io"
import { SwitchLanguage } from "~/components"
import { useT } from "~/hooks"
import {
  initialLocalSettings,
  local,
  type LocalSetting,
  type LocalSettingGroup,
  resetLocalSettingsGroup,
  setLocal,
  userCan,
} from "~/store"
import { bus } from "~/utils"
import { isMobile } from "~/utils/compatibility"
import { normalizeLocalNumber } from "~/utils/local-settings-policy"
import { uxText } from "~/utils/ux"
import "./local-settings.css"

const groupTitle = (group: LocalSettingGroup): string => {
  switch (group) {
    case "appearance":
      return uxText("外观与浏览", "Appearance & browsing", "外觀與瀏覽")
    case "images":
      return uxText("图片阅读", "Image reading", "圖片閱讀")
    case "editor":
      return uxText("编辑器", "Editor", "編輯器")
    case "downloads":
      return uxText(
        "高级下载 · Aria2",
        "Advanced downloads · Aria2",
        "進階下載 · Aria2",
      )
  }
}

function LocalSettingEdit(props: LocalSetting) {
  const t = useT()
  const label = () => t(`home.local_settings.${props.key}`)
  const id = `local-setting-${props.key}`
  const [draft, setDraft] = createSignal(local[props.key] ?? props.default)
  const [feedback, setFeedback] = createSignal("")
  createEffect(() => {
    setDraft(local[props.key] ?? props.default)
    setFeedback("")
  })
  const commitNumber = (value = draft()) => {
    const valid = normalizeLocalNumber(props, value, local[props.key])
    setLocal(props.key, valid)
    setDraft(valid)
    setFeedback(
      valid !== value.trim()
        ? uxText(
            `已调整为 ${valid}`,
            `Adjusted to ${valid}`,
            `已調整為 ${valid}`,
          )
        : "",
    )
  }
  const step = (delta: number) => {
    commitNumber(
      String(
        Number(normalizeLocalNumber(props, draft(), local[props.key])) + delta,
      ),
    )
  }
  const disabled = () => props.key === "open_item_on_checkbox" && isMobile
  const hint = () => {
    if (props.type === "number")
      return uxText(
        `范围：${props.min}–${props.max} px；失焦或按 Enter 保存。`,
        `Range: ${props.min}–${props.max} px; saved on blur or Enter.`,
        `範圍：${props.min}–${props.max} px；失焦或按 Enter 儲存。`,
      )
    switch (props.key) {
      case "global_default_layout":
        return uxText(
          "用于没有单独保存视图的目录。",
          "Used for folders without a saved view.",
          "用於沒有單獨儲存檢視的目錄。",
        )
      case "open_item_on_checkbox":
        return isMobile
          ? uxText(
              "仅桌面鼠标多选时生效；手机保持直接打开。",
              "Only applies to desktop mouse selection; phones open items directly.",
              "僅桌面滑鼠多選時生效；手機保持直接開啟。",
            )
          : uxText(
              "仅开启复选框多选后生效。",
              "Only applies when checkbox selection is enabled.",
              "僅開啟核取方塊多選後生效。",
            )
      case "show_gallery_thumbnails":
        return uxText(
          "仅用于目录图片查看器，不改变独立阅读器进度。",
          "Applies to the folder gallery; independent reader progress is unchanged.",
          "僅用於目錄圖片檢視器，不改變獨立閱讀器進度。",
        )
      case "aria2_rpc_secret":
        return uxText(
          "保存在此浏览器，不会随分组重置清除。请勿在共享设备保存。",
          "Stored in this browser and excluded from group reset. Avoid saving on shared devices.",
          "儲存在此瀏覽器，不會隨分組重設清除。請勿在共用裝置儲存。",
        )
      default:
        return ""
    }
  }
  return (
    <FormControl class="local-setting-field" id={id} disabled={disabled()}>
      <FormLabel for={id}>{label()}</FormLabel>
      <Switch
        fallback={
          <Input
            id={id}
            type={
              props.type === "password"
                ? "password"
                : props.type === "url"
                  ? "url"
                  : "text"
            }
            autocomplete="off"
            spellcheck={false}
            value={local[props.key]}
            aria-describedby={hint() ? `${id}-hint` : undefined}
            onInput={(event) => setLocal(props.key, event.currentTarget.value)}
          />
        }
      >
        <Match when={props.type === "select"}>
          <Select
            id={id}
            value={local[props.key]}
            disabled={disabled()}
            onChange={(value) => setLocal(props.key, value)}
          >
            <SelectTrigger
              id={id}
              aria-label={label()}
              aria-describedby={hint() ? `${id}-hint` : undefined}
            >
              <SelectPlaceholder>{t("global.choose")}</SelectPlaceholder>
              <SelectValue />
              <SelectIcon />
            </SelectTrigger>
            <SelectContent>
              <SelectListbox>
                <For each={props.options}>
                  {(item) => (
                    <SelectOption value={item}>
                      <SelectOptionText>
                        {t(`home.local_settings.${props.key}_options.${item}`)}
                      </SelectOptionText>
                      <SelectOptionIndicator />
                    </SelectOption>
                  )}
                </For>
              </SelectListbox>
            </SelectContent>
          </Select>
        </Match>
        <Match when={props.type === "boolean"}>
          <HopeSwitch
            id={id}
            checked={local[props.key] === "true"}
            onChange={(event: { currentTarget: HTMLInputElement }) =>
              setLocal(props.key, String(event.currentTarget.checked))
            }
          />
        </Match>
        <Match when={props.type === "number"}>
          <div class="local-setting-number">
            <IconButton
              type="button"
              aria-label={`${uxText("减小", "Decrease", "減小")} ${label()}`}
              icon={<FaSolidMinus aria-hidden="true" />}
              disabled={
                Number(
                  normalizeLocalNumber(props, draft(), local[props.key]),
                ) <= props.min!
              }
              onClick={() => step(-1)}
            />
            <Input
              id={id}
              type="number"
              inputmode="numeric"
              min={props.min}
              max={props.max}
              step={1}
              value={draft()}
              onInput={(event) => {
                setDraft(event.currentTarget.value)
                setFeedback("")
              }}
              onBlur={() => commitNumber()}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault()
                  commitNumber()
                }
              }}
              aria-describedby={`${id}-hint ${id}-feedback`}
              class="hide-spin"
            />
            <IconButton
              type="button"
              aria-label={`${uxText("增大", "Increase")} ${label()}`}
              icon={<FaSolidPlus aria-hidden="true" />}
              disabled={
                Number(
                  normalizeLocalNumber(props, draft(), local[props.key]),
                ) >= props.max!
              }
              onClick={() => step(1)}
            />
          </div>
          <span
            id={`${id}-feedback`}
            class="local-setting-feedback"
            role="status"
          >
            {feedback()}
          </span>
        </Match>
      </Switch>
      <Show when={hint()}>
        <p id={`${id}-hint`} class="local-setting-hint">
          {hint()}
        </p>
      </Show>
    </FormControl>
  )
}

function SettingsGroup(props: { group: LocalSettingGroup }) {
  const [status, setStatus] = createSignal("")
  const reset = () => {
    resetLocalSettingsGroup(props.group)
    setStatus(
      uxText(
        "已重置本组；凭据与阅读进度保持不变。",
        "Group reset; credentials and reading progress are unchanged.",
        "已重設本組；憑證與閱讀進度保持不變。",
      ),
    )
  }
  return (
    <section
      class="local-settings-group"
      aria-labelledby={`settings-${props.group}-title`}
    >
      <div class="local-settings-group-heading">
        <h3 id={`settings-${props.group}-title`}>{groupTitle(props.group)}</h3>
        <Button
          type="button"
          size="xs"
          variant="outline"
          onClick={reset}
          aria-label={`${uxText("重置", "Reset", "重設")} ${groupTitle(props.group)}`}
        >
          {uxText("重置本组", "Reset group", "重設本組")}
        </Button>
      </div>
      <Show when={props.group === "editor" && !userCan("write_content")}>
        <p class="local-setting-hint">
          {uxText(
            "这些偏好也用于文本预览；保存文件仍需写入权限。",
            "These preferences also apply to text previews; saving files still requires write permission.",
            "這些偏好也用於文字預覽；儲存檔案仍需寫入權限。",
          )}
        </p>
      </Show>
      <Show when={props.group === "downloads"}>
        <p class="local-setting-hint">
          {uxText(
            "仅“发送到 Aria2”使用，需要此设备能够访问的 RPC 服务；手机上的 localhost 指手机本身。HTTPS 页面可能阻止 HTTP RPC。",
            "Only used by Send to Aria2. A reachable RPC service is required; localhost on a phone means the phone itself. HTTPS pages may block HTTP RPC.",
            "僅「傳送到 Aria2」使用，需要此裝置能夠存取的 RPC 服務；手機上的 localhost 指手機本身。HTTPS 頁面可能阻擋 HTTP RPC。",
          )}
        </p>
      </Show>
      <For
        each={initialLocalSettings.filter(
          (setting) => setting.group === props.group && !setting.hidden,
        )}
      >
        {(setting) => <LocalSettingEdit {...setting} />}
      </For>
      <p class="local-setting-feedback" role="status">
        {status()}
      </p>
    </section>
  )
}

export const LocalSettings = () => {
  const { isOpen, onOpen, onClose } = createDisclosure()
  const { toggleColorMode } = useColorMode()
  const themeIcon = useColorModeValue(FiMoon, FiSun)
  const t = useT()
  const handler = (name: string) => {
    if (name === "local_settings") onOpen()
  }
  bus.on("tool", handler)
  onCleanup(() => bus.off("tool", handler))
  return (
    <Drawer
      id="local-settings-dialog"
      opened={isOpen()}
      placement="right"
      onClose={onClose}
      initialFocus="#local-settings-close"
    >
      <DrawerOverlay />
      <DrawerContent class="local-settings-drawer">
        <DrawerCloseButton
          id="local-settings-close"
          aria-label={uxText(
            "关闭本地设置",
            "Close local settings",
            "關閉本機設定",
          )}
        />
        <DrawerHeader color="$info9">
          {t("home.toolbar.local_settings")}
        </DrawerHeader>
        <DrawerBody>
          <p class="local-setting-hint local-settings-scope">
            {uxText(
              "仅保存在当前浏览器，修改后自动生效，不影响其他设备或服务器。",
              "Saved only in this browser and applied automatically. Other devices and the server are unaffected.",
              "僅儲存在目前瀏覽器，修改後自動生效，不影響其他裝置或伺服器。",
            )}
          </p>
          <div
            class="local-settings-quick"
            role="group"
            aria-label={uxText("主题与语言", "Theme & language", "主題與語言")}
          >
            <SwitchLanguage
              as={IconButton}
              type="button"
              icon={<IoLanguageOutline aria-hidden="true" />}
              aria-label={t("home.toolbar.switch_lang")}
              title={t("home.toolbar.switch_lang")}
            />
            <IconButton
              type="button"
              icon={
                <Show
                  when={themeIcon() === FiMoon}
                  fallback={<FiSun aria-hidden="true" />}
                >
                  <FiMoon aria-hidden="true" />
                </Show>
              }
              aria-label={t("home.toolbar.toggle_theme")}
              title={t("home.toolbar.toggle_theme")}
              onClick={toggleColorMode}
            />
            <span>
              {uxText("主题与语言", "Theme & language", "主題與語言")}
            </span>
          </div>
          <For each={["appearance", "images", "editor"] as const}>
            {(group) => <SettingsGroup group={group} />}
          </For>
          <details class="local-settings-advanced">
            <summary>{groupTitle("downloads")}</summary>
            <SettingsGroup group="downloads" />
          </details>
        </DrawerBody>
      </DrawerContent>
    </Drawer>
  )
}
