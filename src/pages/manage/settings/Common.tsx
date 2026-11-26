import { useFetch, useT, useRouter, useManageTitle } from "~/hooks"
import { Group, SettingItem, PResp, PEmptyResp, EmptyResp, Resp } from "~/types"
import {
  r,
  notify,
  getTarget,
  handleResp,
  handleRespWithoutAuthAndNotify,
} from "~/utils"
import { createStore } from "solid-js/store"
import { Badge, Button, HStack, useColorModeValue, VStack } from "@hope-ui/solid"
import { createSignal, Index, onCleanup, onMount, Show } from "solid-js"
import { Item } from "./SettingItem"
import { ResponsiveGrid } from "../common/ResponsiveGrid"
import { setSettings as renewSettings } from "~/store"
import { withHeadWriteLock } from "~/utils/head-write-lock"
import { uxText } from "~/utils/ux"

import {
  homeGlassSettings,
  isHomeGlassSetting,
  loadHomeGlassSettings,
  saveHomeGlassSettings,
  validateHomeGlassSettings,
} from "./home-glass-setting"
import {
  editableSettings,
  prepareSettingsSave,
  verifyHeadSave,
} from "./managed-head"

export interface CommonSettingsProps {
  group: Group
}
const CommonSettings = (props: CommonSettingsProps) => {
  const t = useT()
  const { pathname } = useRouter()
  useManageTitle(`manage.sidemenu.${pathname().split("/").pop()}`)
  const [settingsLoading, getSettings] = useFetch((): PResp<SettingItem[]> =>
    r.get(`/admin/setting/list?group=${props.group}`),
  )
  const [settings, setSettings] = createStore<SettingItem[]>([])
  const [dirty, setDirty] = createSignal(false)
  const refresh = async () => {
    try {
      const resp = await getSettings()
      if (resp.code === 200 && props.group === Group.STYLE) {
        resp.data.push(...(await loadHomeGlassSettings()))
      }
      handleResp<SettingItem[]>(resp, (data) => {
        setSettings(editableSettings(data))
        setDirty(false)
      })
    } catch (error) {
      notify.error(String(error))
    }
  }
  refresh()
  const [saveLoading, saveSettings] = useFetch(async (): PEmptyResp => {
    const items = getTarget(settings) as SettingItem[]
    const performSave = async (): PEmptyResp => {
      if (props.group === Group.STYLE) {
        try {
          validateHomeGlassSettings(items)
        } catch (error) {
          return { code: 400, message: String(error), data: {} }
        }
      }
      let payload: SettingItem[]
      try {
        payload = await prepareSettingsSave(
          items.filter(
            (item) =>
              item.key !== "folder_icon_style" && !isHomeGlassSetting(item.key),
          ),
        )
      } catch (error) {
        return { code: 500, message: String(error), data: {} }
      }
      const resp: EmptyResp = await r.post("/admin/setting/save", payload)
      if (resp.code === 200) {
        try {
          await verifyHeadSave(payload)
        } catch (error) {
          return { code: 500, message: String(error), data: {} }
        }
      }

      if (resp.code === 200 && props.group === Group.STYLE) {
        try {
          await saveHomeGlassSettings(items)
        } catch (error) {
          return { code: 500, message: String(error), data: {} }
        }
      }
      return resp
    }
    try {
      return items.some((item) => item.key === "customize_head") ||
        props.group === Group.STYLE
        ? await withHeadWriteLock(performSave)
        : await performSave()
    } catch (error) {
      return { code: 500, message: String(error), data: {} }
    }
  })
  const [defaultLoading, defaultSettings] = useFetch((): PResp<SettingItem[]> =>
    r.post(`/admin/setting/default?group=${props.group}`),
  )
  const [loading, setLoading] = createSignal(false)
  const barBg = useColorModeValue("rgba(255, 255, 255, 0.92)", "rgba(24, 26, 32, 0.92)")
  const performSaveClick = async () => {
    if (saveLoading()) return
    const resp = await saveSettings()
    handleResp(resp, async () => {
      setDirty(false)
      notify.success(t("global.save_success"))
      handleRespWithoutAuthAndNotify(
        (await r.get("/public/settings")) as Resp<Record<string, string>>,
        renewSettings,
      )
    })
  }
  onMount(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
        event.preventDefault()
        void performSaveClick()
      }
    }
    document.addEventListener("keydown", onKeyDown)
    onCleanup(() => document.removeEventListener("keydown", onKeyDown))
  })
  return (
    <VStack w="$full" alignItems="start" spacing="$2">
      <ResponsiveGrid>
        <Index each={settings}>
          {(item) => (
            <Item
              {...item()}
              onChange={(val) => {
                setSettings((i) => item().key === i.key, "value", val)
                setDirty(true)
              }}
              onDelete={async () => {
                setLoading(true)
                const resp: EmptyResp = await r.post(
                  `/admin/setting/delete?key=${item().key}`,
                )
                setLoading(false)
                handleResp(resp, () => {
                  notify.success(t("global.delete_success"))
                  refresh()
                })
              }}
            />
          )}
        </Index>
      </ResponsiveGrid>
      <HStack
        w="$full"
        spacing="$2"
        gap="$2"
        wrap="wrap"
        alignItems="center"
        pos="sticky"
        bottom="0"
        py="$3"
        px="$3"
        mt="$2"
        rounded="$lg"
        bgColor={barBg()}
        border="1px solid $neutral6"
        shadow="$sm"
        css={{ backdropFilter: "blur(8px)" }}
        zIndex="$docked"
      >
        <Button
          loading={saveLoading()}
          onClick={() => void performSaveClick()}
        >
          {t("global.save")}
        </Button>
        <Button
          colorScheme="accent"
          onClick={refresh}
          loading={settingsLoading() || loading() || defaultLoading()}
        >
          {t("global.refresh")}
        </Button>
        <Button
          colorScheme="warning"
          loading={settingsLoading() || loading() || defaultLoading()}
          onClick={async () => {
            const resp = await defaultSettings()
            handleResp(resp, (data) => {
              notify.info(t("manage.load_default_setting_success"))
              if (props.group === Group.STYLE) {
                data.push(...homeGlassSettings())
              }
              setSettings(editableSettings(data))
              setDirty(true)
            })
          }}
        >
          {t("manage.load_default_setting")}
        </Button>
        <Show when={dirty()}>
          <Badge colorScheme="warning" ml="auto">
            {uxText("有未保存修改 (Ctrl+S 保存)", "Unsaved changes (Ctrl+S)", "有未儲存變更 (Ctrl+S 儲存)")}
          </Badge>
        </Show>
      </HStack>
    </VStack>
  )
}

export default CommonSettings
