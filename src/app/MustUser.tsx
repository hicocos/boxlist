import {
  createEffect,
  createSignal,
  JSXElement,
  Match,
  on,
  onCleanup,
  onMount,
  Switch,
} from "solid-js"
import { Error } from "~/components"
import { useFetch, useRouter, useT } from "~/hooks"
import { Me, me, setMe } from "~/store"
import { HistoryMap, releaseHistoryLocation } from "~/store/history"
import { PResp } from "~/types"
import { r, handleResp, handleRespWithoutAuthAndNotify } from "~/utils"
import { FullScreenLoading } from "~/components/FullLoading"

const MustUser = (props: { children: JSXElement }) => {
  const t = useT()
  const [loading, data] = useFetch((): PResp<Me> => r.get("/me"), true)
  const [err, setErr] = createSignal<string>()
  const rejectIdentity = (message: string) => {
    // Invalidate snapshots even when /me fails without returning a new user.
    releaseHistoryLocation()
    HistoryMap.clear()
    setErr(message)
  }
  const { pathname } = useRouter()
  // Revalidate on every route transition without tearing down an already
  // authorized outlet. The scoped directory cache is invalidated by setMe.
  // A failed check must hide the outlet rather than trust stale identity.
  let check = 0
  onCleanup(() => check++)
  createEffect(
    on(
      pathname,
      async () => {
        if (loading()) return // initial onMount check already covers this route
        const generation = ++check
        try {
          const response = (await r.get("/me")) as Awaited<PResp<Me>>
          if (generation !== check) return
          handleResp(
            response,
            (user) => {
              // Avoid restarting in-flight directory effects for an unchanged
              // identity (me is a signal, not a structurally compared store).
              if (JSON.stringify(user) !== JSON.stringify(me())) setMe(user)
              setErr(undefined)
            },
            rejectIdentity,
          )
        } catch {
          if (generation === check) rejectIdentity("Unable to verify current user")
        }
      },
      { defer: true },
    ),
  )
  onMount(async () => {
    handleResp(await data(), setMe, setErr)
  })
  return (
    <Switch fallback={props.children}>
      <Match when={loading()}>
        <FullScreenLoading />
      </Match>
      <Match when={err() !== undefined}>
        <Error msg={t("home.get_current_user_failed") + err()} />
      </Match>
    </Switch>
  )
}

const UserOrGuest = (props: { children: JSXElement }) => {
  // 将loading默认设置为true，修复children被提前渲染，明显症状：两个公告
  const [loading, data] = useFetch((): PResp<Me> => r.get("/me"), true)
  const [skipLogin, setSkipLogin] = createSignal(false)
  onMount(async () => {
    handleRespWithoutAuthAndNotify(await data(), setMe, (_msg, _code) => {
      setMe({
        id: 2,
        username: "guest",
        password: "",
        base_path: "/",
        role: 1,
        disabled: false,
        permission: 0,
        sso_id: "",
        otp: false,
        allow_ldap: false,
      })
      setSkipLogin(true)
    })
  })
  return (
    <Switch fallback={props.children}>
      <Match when={!skipLogin() && loading()}>
        <FullScreenLoading />
      </Match>
    </Switch>
  )
}

export { MustUser, UserOrGuest }
