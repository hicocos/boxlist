import { Progress, ProgressIndicator } from "@hope-ui/solid"
import { Route, Routes, useIsRouting } from "@solidjs/router"
import {
  Component,
  createEffect,
  createSignal,
  lazy,
  Match,
  onCleanup,
  onMount,
  Switch,
} from "solid-js"
import { Portal } from "solid-js/web"
import { Error, FullScreenLoading } from "~/components"
import { useLoading, useRouter, useT } from "~/hooks"
import { setSettings } from "~/store"
import { setArchiveExtensions } from "~/store/archive"
import { Resp } from "~/types"
import { base_path, bus, handleRespWithoutAuthAndNotify, r } from "~/utils"
import { MustUser, UserOrGuest } from "./MustUser"
import "./index.css"
import "./glass-theme.css"
import { bootstrapGlassTheme, listenForHomeGlassSettings } from "./glass-theme"
import { globalStyles } from "./theme"
import {
  mountBackgroundPriority,
  refreshBackgroundPriority,
} from "./background-priority"

const Home = lazy(() => import("~/pages/home/Layout"))
const Manage = lazy(() => import("~/pages/manage"))
const Login = lazy(() => import("~/pages/login"))
const Test = lazy(() => import("~/pages/test"))
const Reader = lazy(() => import("~/pages/reader/Reader"))

const App: Component = () => {
  const t = useT()
  globalStyles()
  const isRouting = useIsRouting()
  const { to, pathname } = useRouter()
  bootstrapGlassTheme()
  let backgroundLayer!: HTMLDivElement
  onMount(() => {
    onCleanup(mountBackgroundPriority(backgroundLayer))
  })
  onCleanup(listenForHomeGlassSettings())
  const onTo = (path: string) => {
    to(path)
  }
  bus.on("to", onTo)
  onCleanup(() => {
    bus.off("to", onTo)
  })

  createEffect(() => {
    bus.emit("pathname", pathname())
    document.documentElement.classList.toggle(
      "openlist-glass-home",
      !pathname().startsWith("/@manage") && pathname() !== "/@reader",
    )
    document.documentElement.classList.toggle(
      "openlist-reader-page",
      pathname() === "/@reader",
    )
    refreshBackgroundPriority()
  })

  const [err, setErr] = createSignal<string[]>([])
  const [loading, data] = useLoading(() =>
    Promise.all([
      (async () => {
        handleRespWithoutAuthAndNotify(
          (await r.get("/public/settings")) as Resp<Record<string, string>>,
          setSettings,
          (e) => setErr(err().concat(e)),
        )
      })(),
      (async () => {
        handleRespWithoutAuthAndNotify(
          (await r.get("/public/archive_extensions")) as Resp<string[]>,
          setArchiveExtensions,
          (e) => setErr(err().concat(e)),
        )
      })(),
    ]),
  )
  data()
  return (
    <>
      <Portal>
        <div ref={backgroundLayer} class="home-background" aria-hidden="true" />
        <Progress
          indeterminate
          size="xs"
          position="fixed"
          top="0"
          left="0"
          right="0"
          zIndex="$banner"
          d={isRouting() ? "block" : "none"}
        >
          <ProgressIndicator />
        </Progress>
      </Portal>
      <Switch
        fallback={
          <Routes base={base_path}>
            <Route path="/@test" component={Test} />
            <Route path="/@login" component={Login} />
            <Route
              path="/@reader"
              element={
                <MustUser>
                  <Reader />
                </MustUser>
              }
            />
            <Route
              path="/@manage/*"
              element={
                <MustUser>
                  <Manage />
                </MustUser>
              }
            />
            <Route
              path={["/@s/*", "/%40s/*"]}
              element={
                <UserOrGuest>
                  <Home />
                </UserOrGuest>
              }
            />
            <Route
              path="*"
              element={
                <MustUser>
                  <Home />
                </MustUser>
              }
            />
          </Routes>
        }
      >
        <Match when={err().length > 0}>
          <Error
            h="100vh"
            msg={
              t("home.fetching_settings_failed") +
              err()
                .map((e) => t("home." + e))
                .join(", ")
            }
          />
        </Match>
        <Match when={loading()}>
          <FullScreenLoading />
        </Match>
      </Switch>
    </>
  )
}

export default App
