import { Box, ElementType, Icon, IconProps, Tooltip } from "@hope-ui/solid"
import { IconTypes } from "solid-icons"
import { splitProps, type JSX } from "solid-js"
import { useT } from "~/hooks"
import { getMainColor, me } from "~/store"
import { UserMethods, UserPermissions } from "~/types"
import { hoverColor } from "~/utils"
import { operations } from "./operations"
import "./toolbar-actions.css"

/** `as` still names the glyph. DOM handlers/ref/ARIA belong to the button. */
type ToolbarIconProps<C extends ElementType = "button"> = Omit<
  IconProps<C>,
  keyof JSX.ButtonHTMLAttributes<HTMLButtonElement>
> &
  JSX.ButtonHTMLAttributes<HTMLButtonElement> & {
    name?: string
    tips?: string
    icon?: IconTypes
  }

function ToolbarAction<C extends ElementType = "button">(
  props: ToolbarIconProps<C> & { variant: "center" | "right" },
) {
  const t = useT()
  const [glyph, rest] = splitProps(props, [
    "as",
    "icon",
    "children",
    "name",
    "tips",
    "variant",
    "class",
    "boxSize",
    "p",
    "pl",
    "pr",
    "pt",
    "pb",
    "px",
    "py",
  ])
  const operation = () => operations[glyph.name ?? ""]
  const label = () =>
    props["aria-label"] ??
    t(`home.toolbar.${glyph.tips ?? glyph.name ?? "more"}`)
  const right = () => glyph.variant === "right"
  return (
    <Tooltip
      disabled={right() && !glyph.tips && !props["aria-label"]}
      placement={right() ? "left" : "top"}
      withArrow
      label={label()}
    >
      <Box
        color={right() ? getMainColor() : operation()?.color}
        _hover={
          right()
            ? { bgColor: getMainColor(), color: "white" }
            : { bgColor: hoverColor() }
        }
        rounded={right() ? "$lg" : "$md"}
        {...(rest as IconProps<"button">)}
        as="button"
        type="button"
        class={`toolbar-action toolbar-action--${glyph.variant}${glyph.name ? ` toolbar-${glyph.name}` : ""}${glyph.class ? ` ${glyph.class}` : ""}`}
        aria-label={label()}
        title={props.title ?? label()}
        data-danger={
          glyph.name === "delete" || props.color === "$danger9"
            ? "true"
            : undefined
        }
      >
        <Icon
          as={(glyph.as ?? glyph.icon ?? operation()?.icon) as ElementType}
          boxSize={glyph.boxSize ?? (right() ? "$8" : "$7")}
          p={glyph.p ?? (operation()?.p ? "$1_5" : "$1")}
          pl={glyph.pl}
          pr={glyph.pr}
          pt={glyph.pt}
          pb={glyph.pb}
          px={glyph.px}
          py={glyph.py}
          aria-hidden="true"
          color="inherit"
        >
          {glyph.children}
        </Icon>
      </Box>
    </Tooltip>
  )
}

export const CenterIcon = <C extends ElementType = "button">(
  props: ToolbarIconProps<C> & { name: string },
) => {
  const index = UserPermissions.findIndex(
    (permission) => permission === props.name,
  )
  if (index !== -1 && !UserMethods.can(me(), index)) return null
  return <ToolbarAction {...props} variant="center" />
}

export const RightIcon = <C extends ElementType = "button">(
  props: ToolbarIconProps<C>,
) => <ToolbarAction {...props} variant="right" />
