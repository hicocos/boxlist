import { Progress, ProgressIndicator } from "@hope-ui/solid"
import { Outlet, Route, Routes, useIsRouting } from "@solidjs/router"
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
import { Error } from "~/components"
import { useLoading, useRouter, useT } from "~/hooks"
import { setSettings } from "~/store"
import { loadArchiveExtensions } from "~/store/archive"
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
import { FullScreenLoading } from "~/components/FullLoading"

const Home = lazy(() => import("~/pages/home/Layout"))
const Manage = lazy(() => import("~/pages/manage"))
const Login = lazy(() => import("~/pages/login"))
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
    const loadArchives = () => { void loadArchiveExtensions().catch(console.warn) }
    window.addEventListener("openlist:primary-ready", loadArchives, { once: true })
    onCleanup(() => window.removeEventListener("openlist:primary-ready", loadArchives))
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
  const [loading, data] = useLoading(async () => {
    setErr([])
    try {
      handleRespWithoutAuthAndNotify(
        (await r.get("/public/settings")) as Resp<Record<string, string>>,
        setSettings,
        (e) => setErr([e]),
      )
    } catch {
      setErr(["连接失败，请检查网络后重试。"])
    }
  })
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
            <Route path="/@login" component={Login} />
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
            {/* Keep the identity boundary mounted across directory/reader navigation.
                Separate guards replaced the page with a full-screen /me loader. */}
            <Route
              path="/"
              element={
                <MustUser>
                  <Outlet />
                </MustUser>
              }
            >
              <Route path="/@reader" component={Reader} />
              <Route path="*" component={Home} />
            </Route>
          </Routes>
        }
      >
        <Match when={err().length > 0}>
          <div>
          <Error
            h="60vh"
            msg={
              t("home.fetching_settings_failed") +
              err()
                .map((e) => t("home." + e))
                .join(", ")
            }
          />
          <button type="button" onClick={() => void data()}>重试</button>
          </div>
        </Match>
        <Match when={loading()}>
          <FullScreenLoading />
        </Match>
      </Switch>
    </>
  )
}

export default App
