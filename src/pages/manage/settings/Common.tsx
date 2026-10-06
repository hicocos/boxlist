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
import { Button, HStack, VStack } from "@hope-ui/solid"
import { createSignal, Index } from "solid-js"
import { Item } from "./SettingItem"
import { ResponsiveGrid } from "../common/ResponsiveGrid"
import { setSettings as renewSettings } from "~/store"
import { withHeadWriteLock } from "~/utils/head-write-lock"

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
  const refresh = async () => {
    try {
      const resp = await getSettings()
      if (resp.code === 200 && props.group === Group.STYLE) {
        resp.data.push(...(await loadHomeGlassSettings()))
      }
      handleResp<SettingItem[]>(resp, (data) =>
        setSettings(editableSettings(data)),
      )
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
  return (
    <VStack w="$full" alignItems="start" spacing="$2">
      <ResponsiveGrid>
        <Index each={settings}>
          {(item) => (
            <Item
              {...item()}
              onChange={(val) => {
                setSettings((i) => item().key === i.key, "value", val)
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
      <HStack spacing="$2">
        <Button
          colorScheme="accent"
          onClick={refresh}
          loading={settingsLoading() || loading() || defaultLoading()}
        >
          {t("global.refresh")}
        </Button>
        <Button
          loading={saveLoading()}
          onClick={async () => {
            const resp = await saveSettings()
            handleResp(resp, async () => {
              notify.success(t("global.save_success"))
              handleRespWithoutAuthAndNotify(
                (await r.get("/public/settings")) as Resp<
                  Record<string, string>
                >,
                renewSettings,
              )
            })
          }}
        >
          {t("global.save")}
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
            })
          }}
        >
          {t("manage.load_default_setting")}
        </Button>
      </HStack>
    </VStack>
  )
}

export default CommonSettings
