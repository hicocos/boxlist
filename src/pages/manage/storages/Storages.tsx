import {
  Box,
  Button,
  Grid,
  HStack,
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
  Table,
  Tbody,
  Th,
  Thead,
  Tr,
  VStack,
  Switch as HopeSwitch,
} from "@hope-ui/solid"
import { createMemo, createSignal, For, Match, Show, Switch } from "solid-js"
import { useFetch, useManageTitle, useRouter, useT } from "~/hooks"
import { handleResp, notify, r } from "~/utils"
import { uxText } from "~/utils/ux"
import { EmptyResp, PageResp, Resp, Storage } from "~/types"
import { StorageGridItem, StorageListItem } from "./Storage"
import { createStorageSignal } from "@solid-primitives/storage"

const Storages = () => {
  const t = useT()
  useManageTitle("manage.sidemenu.storages")
  const { to } = useRouter()
  const [getStoragesLoading, getStorages] = useFetch(
    (): Promise<PageResp<Storage>> => r.get("/admin/storage/list"),
  )
  const [storages, setStorages] = createSignal<Storage[]>([])
  const refresh = async () => {
    const resp = await getStorages()
    handleResp(resp, (data) => setStorages(data.content))
  }
  const [drivers, setDrivers] = createSignal<string[]>([])
  const [selectedDrivers, setSelectedDrivers] = createSignal<string[]>([])
  const [keyword, setKeyword] = createSignal("")
  const getDrivers = async () => {
    const resp: Resp<string[]> = await r.get("/admin/driver/names")
    handleResp(resp, (data) => setDrivers(data))
  }
  getDrivers()
  refresh()
  const loadAll = async () => {
    const resp: EmptyResp = await r.post("/admin/storage/load_all")
    handleResp(resp, () => {
      notify.success(t("storages.other.start_load_success"))
    })
  }
  const shownStorages = createMemo(() => {
    const kw = keyword().trim().toLowerCase()
    return storages().filter((storage) => {
      if (
        selectedDrivers().length > 0 &&
        !selectedDrivers().includes(storage.driver)
      ) {
        return false
      }
      if (!kw) return true
      const driverName = t(`drivers.drivers.${storage.driver}`).toLowerCase()
      return (
        storage.mount_path.toLowerCase().includes(kw) ||
        storage.driver.toLowerCase().includes(kw) ||
        driverName.includes(kw) ||
        (storage.remark || "").toLowerCase().includes(kw)
      )
    })
  })
  const [layout, setLayout] = createStorageSignal(
    "storages-layout",
    "grid" as "grid" | "table",
  )
  return (
    <VStack spacing="$3" alignItems="start" w="$full">
      <HStack
        spacing="$2"
        gap="$2"
        w="$full"
        wrap="wrap"
        alignItems="center"
      >
        <Button
          colorScheme="accent"
          flexShrink={0}
          loading={getStoragesLoading()}
          onClick={refresh}
        >
          {t("global.refresh")}
        </Button>
        <Button
          flexShrink={0}
          onClick={() => {
            to("/@manage/storages/add")
          }}
        >
          {t("global.add")}
        </Button>
        <Button
          colorScheme="warning"
          flexShrink={0}
          loading={getStoragesLoading()}
          onClick={loadAll}
        >
          {t("storages.other.load_all")}
        </Button>
        <Input
          type="search"
          w={{ "@initial": "$full", "@md": "220px" }}
          flex={{ "@initial": "1 1 180px", "@md": "0 1 220px" }}
          value={keyword()}
          placeholder={uxText("搜索挂载路径、备注…", "Search mount path, remark…", "搜尋掛載路徑、備註…")}
          aria-label={uxText("搜索存储", "Search storages", "搜尋儲存")}
          onInput={(e) => setKeyword(e.currentTarget.value)}
        />
        <Show when={drivers().length > 0}>
          <Box w={{ "@initial": "$full", "@md": "240px" }} flex={{ "@initial": "1 1 200px", "@md": "0 1 240px" }} minW="0">
            <Select
              multiple
              value={selectedDrivers()}
              onChange={setSelectedDrivers}
            >
              <SelectTrigger>
                <SelectPlaceholder>
                  {t("storages.other.filter_by_driver")}
                </SelectPlaceholder>
                <SelectValue />
                <SelectIcon />
              </SelectTrigger>
              <SelectContent>
                <SelectListbox>
                  <For each={drivers()}>
                    {(item) => (
                      <SelectOption value={item}>
                        <SelectOptionText>
                          {t(`drivers.drivers.${item}`)}
                        </SelectOptionText>
                        <SelectOptionIndicator />
                      </SelectOption>
                    )}
                  </For>
                </SelectListbox>
              </SelectContent>
            </Select>
          </Box>
        </Show>
        <HopeSwitch
          flexShrink={0}
          css={{ whiteSpace: "nowrap" }}
          checked={layout() === "table"}
          onChange={(e: Event) => {
            setLayout(
              (e.currentTarget as HTMLInputElement).checked ? "table" : "grid",
            )
          }}
        >
          {t("storages.other.table_layout")}
        </HopeSwitch>
      </HStack>
      <Switch>
        <Match when={layout() === "grid"}>
          <Grid
            w="$full"
            gap="$2_5"
            templateColumns={{
              "@initial": "1fr",
              "@lg": "repeat(auto-fill, minmax(324px, 1fr))",
            }}
          >
            <For each={shownStorages()}>
              {(storage) => (
                <StorageGridItem storage={storage} refresh={refresh} />
              )}
            </For>
          </Grid>
        </Match>
        <Match when={layout() === "table"}>
          <Box w="$full" overflowX="auto">
            <Table highlightOnHover dense>
              <Thead>
                <Tr>
                  <For
                    each={[
                      "mount_path",
                      "driver",
                      "order",
                      "usage",
                      "status",
                      "remark",
                    ]}
                  >
                    {(title) => <Th>{t(`storages.common.${title}`)}</Th>}
                  </For>
                  <Th>{t("global.operations")}</Th>
                </Tr>
              </Thead>
              <Tbody>
                <For each={shownStorages()}>
                  {(storage) => (
                    <StorageListItem storage={storage} refresh={refresh} />
                  )}
                </For>
              </Tbody>
            </Table>
          </Box>
        </Match>
      </Switch>
    </VStack>
  )
}

export default Storages
