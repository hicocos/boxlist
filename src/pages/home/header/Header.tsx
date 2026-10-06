import {
  HStack,
  useColorModeValue,
  Image,
  Center,
  IconButton,
  CenterProps,
} from "@hope-ui/solid"
import { changeColor } from "seemly"
import { Show, createMemo } from "solid-js"
import { getMainColor, getSetting, local, me, objStore, State } from "~/store"
import { UserMethods } from "~/types"
import { BsSearch } from "solid-icons/bs"
import { useT } from "~/hooks"
import { CenterLoading, LinkWithBase } from "~/components"
import { Container } from "../Container"
import { bus } from "~/utils"
import { Layout } from "./layout"
import { isMac } from "~/utils/compatibility"
import "./header-actions.css"

export const Header = () => {
  const t = useT()
  const logos = getSetting("logo").split("\n")
  const logo = useColorModeValue(logos[0], logos.pop())

  const stickyProps = createMemo<CenterProps>(() => {
    switch (local["position_of_header_navbar"]) {
      case "sticky":
        return { position: "sticky", zIndex: "$sticky", top: 0 }
      default:
        return { position: undefined, zIndex: undefined, top: undefined }
    }
  })

  return (
    <Center
      {...stickyProps()}
      bgColor="$background"
      class="header"
      w="$full"
      // shadow="$md"
    >
      <Container>
        <HStack
          px="calc(2% + 0.5rem)"
          class="header-content"
          py="$2"
          w="$full"
          justifyContent="space-between"
        >
          <HStack class="header-left" h="44px">
            <LinkWithBase
              class="header-logo-link"
              href={
                UserMethods.is_guest(me())
                  ? "/@login?redirect=%2F%40manage"
                  : "/@manage"
              }
              aria-label={t("home.footer.manage")}
              title={t("home.footer.manage")}
            >
              <Image
                src={logo()!}
                h="$full"
                w="auto"
                fallback={<CenterLoading />}
              />
            </LinkWithBase>
          </HStack>
          <HStack class="header-right" spacing="$2">
            <Show when={objStore.state === State.Folder}>
              <Show when={getSetting("search_index") !== "none"}>
                <IconButton
                  type="button"
                  class="header-action header-search"
                  aria-haspopup="dialog"
                  aria-label={t("home.search.search")}
                  title={`${t("home.search.search")} (${isMac ? "Cmd" : "Ctrl"}+K)`}
                  icon={<BsSearch />}
                  compact
                  size="lg"
                  color={getMainColor()}
                  bgColor={changeColor(getMainColor(), { alpha: 0.15 })}
                  _hover={{
                    bgColor: changeColor(getMainColor(), { alpha: 0.2 }),
                  }}
                  onClick={(event) => {
                    event.currentTarget.focus({ preventScroll: true })
                    bus.emit("tool", "search")
                  }}
                />
              </Show>
              <Layout />
            </Show>
          </HStack>
        </HStack>
      </Container>
    </Center>
  )
}
