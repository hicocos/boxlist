import {
  IconButton,
  Menu,
  MenuContent,
  MenuItem,
  MenuTrigger,
} from "@hope-ui/solid"
import { changeColor } from "seemly"
import { BsGridFill, BsCardImage } from "solid-icons/bs"
import { FaSolidListUl } from "solid-icons/fa"
import { Switch, Match, For } from "solid-js"
import { Dynamic } from "solid-js/web"
import { useT } from "~/hooks"
import { getMainColor, LayoutType, layout, setLayout } from "~/store"
import { uxText } from "~/utils/ux"

const layouts = {
  list: FaSolidListUl,
  grid: BsGridFill,
  image: BsCardImage,
} as const

export const Layout = () => {
  const t = useT()
  return (
    <Menu>
      <MenuTrigger
        as={IconButton}
        type="button"
        class="header-action header-layout"
        color={getMainColor()}
        bgColor={changeColor(getMainColor(), { alpha: 0.15 })}
        _hover={{
          bgColor: changeColor(getMainColor(), { alpha: 0.2 }),
        }}
        aria-label={`${uxText("切换视图", "Switch view", "切換檢視")} · ${t(`home.layouts.${layout()}`)}`}
        title={uxText("切换视图", "Switch view", "切換檢視")}
        compact
        size="lg"
        icon={
          <Switch>
            <Match when={layout() === "list"}>
              <FaSolidListUl />
            </Match>
            <Match when={layout() === "grid"}>
              <BsGridFill />
            </Match>
            <Match when={layout() === "image"}>
              <BsCardImage />
            </Match>
          </Switch>
        }
      ></MenuTrigger>
      <MenuContent>
        <For each={Object.entries(layouts)}>
          {(item) => (
            <MenuItem
              role="menuitemradio"
              aria-checked={layout() === item[0]}
              classList={{ "header-layout-selected": layout() === item[0] }}
              icon={<Dynamic component={item[1]} />}
              onSelect={() => {
                setLayout(item[0] as LayoutType)
              }}
            >
              {t(`home.layouts.${item[0]}`)}
            </MenuItem>
          )}
        </For>
      </MenuContent>
    </Menu>
  )
}
