import { Text, useColorModeValue, VStack, Button } from "@hope-ui/solid"
import {
  createEffect,
  createMemo,
  createSignal,
  lazy,
  Match,
  on,
  onCleanup,
  onMount,
  Show,
  Suspense,
  Switch,
} from "solid-js"
import { LinkWithBase } from "~/components"
import { useObjTitle, usePath, useRouter, useT } from "~/hooks"
import {
  getPagination,
  objStore,
  password,
  setPassword,
  /*layout,*/ State,
  me,
} from "~/store"
import { UserMethods } from "~/types"
import {
  recordCurrentDirectory,
  getHistoryScope,
  releaseHistoryLocation,
} from "~/store/history"
import {
  cancelDirectoryRequests,
  directoryFailure,
  directoryBusy,
} from "~/store/directory-navigation"
import { validPage } from "~/store/navigation-state"
import { DirectoryError } from "./DirectoryError"
import "./directory-navigation.css"
import { FullLoading } from "~/components/FullLoading"
import { uxText } from "~/utils/ux"

const Folder = lazy(() => import("./folder/Folder"))
const File = lazy(() => import("./file/File"))
const Password = lazy(() => import("./Password"))
// const ListSkeleton = lazy(() => import("./Folder/ListSkeleton"));
// const GridSkeleton = lazy(() => import("./Folder/GridSkeleton"));

const [objBoxRef, setObjBoxRef] = createSignal<HTMLDivElement>()
export { objBoxRef }

export const Obj = () => {
  const t = useT()
  const cardBg = useColorModeValue("white", "$neutral3")
  const { pathname, searchParams, isShare, to } = useRouter()
  const { handlePathChange, refresh } = usePath()
  const pagination = getPagination()
  const page = createMemo(() => {
    return pagination.type === "pagination"
      ? validPage(searchParams["page"])
      : undefined
  })
  const [retainedHeight, setRetainedHeight] = createSignal(240)
  onMount(() => {
    const element = objBoxRef()
    if (!element) return
    const observer = new ResizeObserver(() => {
      if (
        [State.Folder, State.File].includes(objStore.state) &&
        !directoryBusy()
      ) {
        setRetainedHeight(Math.max(240, element.getBoundingClientRect().height))
      }
    })
    observer.observe(element)
    onCleanup(() => observer.disconnect())
  })
  onCleanup(() => {
    recordCurrentDirectory()
    releaseHistoryLocation()
    cancelDirectoryRequests()
  })
  createEffect(
    on([pathname, page, getHistoryScope], async ([pathname, page]) => {
      recordCurrentDirectory()
      if (searchParams["pwd"]) {
        setPassword(searchParams["pwd"])
      }

      useObjTitle()
      await handlePathChange(pathname, page)
    }),
  )

  const shouldShowStorageButton = createMemo(() => {
    return directoryFailure()?.kind === "other" && UserMethods.is_admin(me())
  })

  const storageErrorActions = () => (
    <Button colorScheme="accent" onClick={() => to("/@manage/storages")}>
      {t("global.go_to_storages")}
    </Button>
  )
  return (
    <VStack
      ref={(el: HTMLDivElement) => setObjBoxRef(el)}
      class="obj-box"
      w="$full"
      rounded="$xl"
      bgColor={cardBg()}
      p="$2"
      shadow="$lg"
      spacing="$2"
    >
      <Suspense
        fallback={
          <div
            class="directory-loading-region"
            style={{ "min-height": `${retainedHeight()}px` }}
          >
            <FullLoading />
          </div>
        }
      >
        <Switch>
          <Match when={objStore.err}>
            <DirectoryError
              failure={directoryFailure()}
              retry={() => refresh()}
              actions={
                shouldShowStorageButton() ? storageErrorActions() : undefined
              }
            />
          </Match>
          <Match
            when={[State.FetchingObj, State.FetchingObjs].includes(
              objStore.state,
            )}
          >
            <div
              class="directory-loading-region"
              style={{ "min-height": `${retainedHeight()}px` }}
            >
              <FullLoading />
            </div>
          </Match>
          <Match when={objStore.state === State.NeedPassword}>
            <Password
              title={
                isShare()
                  ? t("shares.input_password")
                  : t("home.input_password")
              }
              password={password}
              setPassword={setPassword}
              enterCallback={() => refresh(true)}
            >
              <Show when={!isShare()}>
                <Text>{t("global.have_account")}</Text>
                <Text
                  color="$info9"
                  as={LinkWithBase}
                  href={`/@login?redirect=${encodeURIComponent(
                    location.pathname,
                  )}`}
                >
                  {t("global.go_login")}
                </Text>
              </Show>
            </Password>
          </Match>
          <Match when={objStore.state === State.Folder && objStore.total === 0 && !isShare() && pathname() === "/"}>
            <VStack role="status" w="$full" p="$6" spacing="$3">
              <Text>{uxText("此目录暂无文件。", "This folder is empty.", "此目錄暫無檔案。")}</Text>
              <Show when={!isShare() && pathname() === "/" && UserMethods.is_admin(me())}>
                <Text textAlign="center">
                  {uxText(
                    "首次使用？请先在管理页面添加存储，再返回首页浏览文件。",
                    "Getting started? Add a storage in management, then return here to browse your files.",
                    "首次使用？請先在管理頁面新增儲存，再返回首頁瀏覽檔案。",
                  )}
                </Text>
                {storageErrorActions()}
              </Show>
            </VStack>
          </Match>
          <Match
            when={[State.Folder, State.FetchingMore].includes(objStore.state)}
          >
            <Folder />
          </Match>
          <Match when={objStore.state === State.File}>
            <File />
          </Match>
        </Switch>
      </Suspense>
    </VStack>
  )
}
