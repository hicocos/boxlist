import {
  Button,
  Center,
  Flex,
  Heading,
  Image,
  Input,
  Text,
  useColorModeValue,
  VStack,
} from "@hope-ui/solid"
import { createMemo, createSignal, onMount, Show } from "solid-js"
import { SwitchColorMode, SwitchLanguageWhite } from "~/components"
import { useLoading, useT, useTitle } from "~/hooks"
import { getSetting } from "~/store"
import type { EmptyResp, InitSetupRequest, InitStatus, Resp } from "~/types"
import { base_path, r } from "~/utils"
import LoginBg from "../login/LoginBg"

const Init = () => {
  const t = useT()
  useTitle(
    createMemo(() => `${t("init.setup_to")} ${getSetting("site_title")}`),
  )
  const logos = getSetting("logo")
    .split("\n")
    .map((value) => value.trim())
    .filter(Boolean)
  const fallbackLogo = "https://mikupan.com/f/rGLVSK/boxlist-logo.png"
  const logo = useColorModeValue(
    logos[0] || fallbackLogo,
    logos.at(-1) || fallbackLogo,
  )
  const bgColor = useColorModeValue("white", "$neutral1")
  const [username, setUsername] = createSignal("admin")
  const [password, setPassword] = createSignal("")
  const [confirmation, setConfirmation] = createSignal("")
  const [siteTitle, setSiteTitle] = createSignal(
    getSetting("site_title") || "BoxList",
  )
  const [failure, setFailure] = createSignal("")
  const [completed, setCompleted] = createSignal(false)

  onMount(async () => {
    try {
      const resp = await r.get<InitStatus, Resp<InitStatus>>(
        "/public/init_status",
      )
      if (resp.code === 200 && resp.data?.initialized === true) {
        window.location.replace(base_path + "/@login")
      } else if (resp.code !== 200) {
        setFailure(resp.message || t("init.failed"))
      }
    } catch (error) {
      setFailure(error instanceof Error ? error.message : t("init.failed"))
    }
  })

  const [loading, setup] = useLoading(() =>
    r.post<EmptyResp, EmptyResp, InitSetupRequest>("/public/init/setup", {
      username: username().trim(),
      // Setup accepts the original password; only the login API uses hashPwd.
      password: password(),
      site_title: siteTitle(),
    }),
  )
  const submit = async (event: SubmitEvent) => {
    event.preventDefault()
    if (loading() || completed()) return
    setFailure("")
    if (password().length < 4) {
      setFailure(t("init.password_too_short"))
      return
    }
    if (password() !== confirmation()) {
      setFailure(t("init.password_mismatch"))
      return
    }
    try {
      const resp = await setup()
      if (resp.code !== 200) {
        setFailure(resp.message || t("init.failed"))
        return
      }
      setPassword("")
      setConfirmation("")
      // Go persists synchronously; it has no Worker env_check/ready polling API.
      setCompleted(true)
    } catch (error) {
      setFailure(error instanceof Error ? error.message : t("init.failed"))
    }
  }
  return (
    <Center zIndex="$docked" w="$full" minH="100vh" py="$6">
      <VStack
        bgColor={bgColor()}
        rounded="$xl"
        p="24px"
        w={{ "@initial": "90%", "@sm": "400px" }}
        spacing="$4"
      >
        <Flex alignItems="center" justifyContent="space-around">
          <Image mr="$2" boxSize="$12" src={logo()} alt="BoxList" />
          <Heading color="$info9" fontSize="$2xl">
            {t("init.title")}
          </Heading>
        </Flex>
        <Show
          when={!completed()}
          fallback={
            <VStack w="$full" spacing="$3">
              <Heading size="lg">{t("init.done_title")}</Heading>
              <Text>{t("init.done_subtitle")}</Text>
              <Text>
                {t("init.done_username")}: {username().trim()}
              </Text>
              {/* Reload to discard App's uninitialized state and refresh settings. */}
              <Button
                w="$full"
                colorScheme="primary"
                onClick={() => window.location.assign(base_path + "/@login")}
              >
                {t("init.done_go_login")}
              </Button>
              <Button
                w="$full"
                onClick={() => window.location.assign(base_path + "/")}
              >
                {t("init.done_go_home")}
              </Button>
            </VStack>
          }
        >
          <form onSubmit={submit} style={{ width: "100%" }}>
            <VStack spacing="$3">
              <Input
                name="username"
                aria-label={t("init.username-tips")}
                placeholder={t("init.username-tips")}
                autocomplete="username"
                required
                value={username()}
                onInput={(e) => setUsername(e.currentTarget.value)}
              />
              <Input
                name="password"
                type="password"
                aria-label={t("init.password-tips")}
                placeholder={t("init.password-tips")}
                autocomplete="new-password"
                required
                value={password()}
                onInput={(e) => setPassword(e.currentTarget.value)}
              />
              <Input
                name="confirm_password"
                type="password"
                aria-label={t("init.confirm_password-tips")}
                placeholder={t("init.confirm_password-tips")}
                autocomplete="new-password"
                required
                value={confirmation()}
                onInput={(e) => setConfirmation(e.currentTarget.value)}
              />
              <Input
                name="site_title"
                aria-label={t("init.site_title-tips")}
                placeholder={t("init.site_title-tips")}
                value={siteTitle()}
                onInput={(e) => setSiteTitle(e.currentTarget.value)}
              />
              <Show when={failure()}>
                <Text
                  role="alert"
                  color="$danger11"
                  w="$full"
                  css={{ overflowWrap: "anywhere" }}
                >
                  {failure()}
                </Text>
              </Show>
              <Button
                type="submit"
                colorScheme="primary"
                w="$full"
                loading={loading()}
              >
                {loading() ? t("init.creating_account") : t("init.setup")}
              </Button>
            </VStack>
          </form>
        </Show>
        <Flex
          justifyContent="space-evenly"
          alignItems="center"
          color="$neutral10"
          w="$full"
        >
          <SwitchLanguageWhite />
          <SwitchColorMode />
        </Flex>
      </VStack>
      <LoginBg />
    </Center>
  )
}

export default Init
