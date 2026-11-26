import {
  Box,
  Button,
  Center,
  createDisclosure,
  Drawer,
  DrawerBody,
  DrawerCloseButton,
  DrawerContent,
  DrawerHeader,
  DrawerOverlay,
  Flex,
  Heading,
  HStack,
  IconButton,
  Tooltip,
  useColorMode,
  useColorModeValue,
} from "@hope-ui/solid"
import { TiThMenu } from "solid-icons/ti"
import { IoExit, IoHome } from "solid-icons/io"
import { FiMoon, FiSun } from "solid-icons/fi"
import { SwitchColorMode, SwitchLanguageWhite } from "~/components"
import { useFetch, useRouter, useT } from "~/hooks"
import { SideMenu } from "./SideMenu"
import { side_menu_items } from "./sidemenu_items"
import { changeToken, handleResp, joinBase, notify, r } from "~/utils"
import { PResp } from "~/types"
const { isOpen, onOpen, onClose } = createDisclosure()
const [logOutReqLoading, logOutReq] = useFetch((): PResp<any> =>
  r.get("/auth/logout"),
)

const Header = () => {
  const t = useT()
  const { to } = useRouter()
  const { toggleColorMode } = useColorMode()
  const themeIcon = useColorModeValue(FiMoon, FiSun)
  const logOut = async () => {
    handleResp(await logOutReq(), () => {
      changeToken()
      notify.success(t("manage.logout_success"))
      to(`/@login?redirect=${encodeURIComponent(location.pathname)}`)
    })
  }
  return (
    <Box
      as="header"
      position="sticky"
      top="0"
      left="0"
      right="0"
      zIndex="$sticky"
      height="64px"
      flexShrink={0}
      shadow="$md"
      p="$4"
      bgColor={useColorModeValue("$background", "$neutral2")()}
    >
      <Flex alignItems="center" justifyContent="space-between" h="$full">
        <HStack spacing="$2">
          <IconButton
            aria-label="menu"
            icon={<TiThMenu />}
            display={{ "@sm": "none" }}
            onClick={onOpen}
            size="sm"
          />
          <Heading
            fontSize="$xl"
            color="$info11"
            cursor="pointer"
            onClick={() => {
              to("/@manage")
            }}
          >
            {t("manage.title")}
          </Heading>
        </HStack>
        <HStack spacing="$2">
          <Tooltip label={t("manage.sidemenu.home")} withArrow>
            <Button
              as="a"
              href={joinBase("/")}
              size="sm"
              variant="subtle"
              colorScheme="neutral"
              leftIcon={<IoHome />}
              aria-label={t("manage.sidemenu.home")}
            >
              {t("manage.sidemenu.home")}
            </Button>
          </Tooltip>
          <Tooltip label={t("home.toolbar.toggle_theme")} withArrow>
            <IconButton
              aria-label={t("home.toolbar.toggle_theme")}
              colorScheme="neutral"
              icon={themeIcon() === FiMoon ? <FiMoon /> : <FiSun />}
              onClick={toggleColorMode}
              size="sm"
            />
          </Tooltip>
          <Tooltip label={t("manage.logout_success").replace(/成功|success/i, "").trim() || "Logout"} withArrow>
            <IconButton
              aria-label="logout"
              icon={<IoExit />}
              loading={logOutReqLoading()}
              onClick={logOut}
              size="sm"
            />
          </Tooltip>
        </HStack>
      </Flex>
      <Drawer opened={isOpen()} placement="left" onClose={onClose}>
        <DrawerOverlay />
        <DrawerContent>
          <DrawerCloseButton />
          <DrawerHeader
            class="manage-drawer-header"
            style={{
              padding: "16px 52px 12px 16px",
            }}
            color="$info11"
          >
            {t("manage.title")}
          </DrawerHeader>
          <DrawerBody>
            <SideMenu items={side_menu_items} />
            <Center>
              <HStack spacing="$4" p="$2" color="$neutral11">
                <SwitchLanguageWhite />
                <SwitchColorMode />
              </HStack>
            </Center>
          </DrawerBody>
        </DrawerContent>
      </Drawer>
    </Box>
  )
}

export { Header, onClose }
