import {
  createEffect,
  createSignal,
  Show,
  splitProps,
  onCleanup,
} from "solid-js"
import { Dynamic } from "solid-js/web"
import type { IconProps, IconTypes } from "solid-icons"
import { IoFolder } from "solid-icons/io"
import { AiOutlineCloudDownload } from "solid-icons/ai"
import { iconAssetById } from "~/utils/icon-assets"
import {
  type IconConfig,
  ICON_CONFIG_EVENT,
  ICON_CONFIG_ID,
  normalizeIconConfig,
  defaultIconConfig,
} from "~/utils/icon-policy"

const readPageIcons = (): IconConfig => {
  const element = document.getElementById(ICON_CONFIG_ID)
  if (element) {
    try {
      return normalizeIconConfig(
        JSON.parse(decodeURIComponent(element.getAttribute("content") ?? "")),
      )
    } catch {
      return defaultIconConfig()
    }
  }
  const legacy = (window as Window & { OPENLIST_FOLDER_ICON_STYLE?: string })
    .OPENLIST_FOLDER_ICON_STYLE
  return legacy === "smile"
    ? { version: 1, selections: { folder: "legacy-smile" } }
    : defaultIconConfig()
}
export const [siteIconConfig, setSiteIconConfig] =
  createSignal<IconConfig>(readPageIcons())
export const publishSiteIconConfig = (config: IconConfig) => {
  const safe = normalizeIconConfig(config)
  let element = document.getElementById(ICON_CONFIG_ID)
  if (!element) {
    element = document.createElement("meta")
    element.id = ICON_CONFIG_ID
    document.head.appendChild(element)
  }
  element.setAttribute("content", encodeURIComponent(JSON.stringify(safe)))
  setSiteIconConfig(safe)
  window.dispatchEvent(new Event(ICON_CONFIG_EVENT))
}

type AssetIconProps = IconProps & { asset?: string; fallback: IconTypes }
export const AssetIcon = (props: AssetIconProps) => {
  const [own, rest] = splitProps(props, ["asset", "fallback", "children"])
  const asset = () => iconAssetById(own.asset ?? "")
  const [failed, setFailed] = createSignal("")
  createEffect(() => {
    own.asset
    setFailed("")
  })
  const retry = () => setFailed("")
  window.addEventListener(ICON_CONFIG_EVENT, retry)
  window.addEventListener("online", retry)
  onCleanup(() => {
    window.removeEventListener(ICON_CONFIG_EVENT, retry)
    window.removeEventListener("online", retry)
  })
  return (
    <Show
      when={asset() && failed() !== asset()?.id}
      fallback={<Dynamic component={own.fallback} {...rest} />}
    >
      <svg
        xmlns="http://www.w3.org/2000/svg"
        width="1em"
        height="1em"
        fill="none"
        {...rest}
        viewBox="0 0 256 256"
        data-custom-icon={asset()?.id}
      >
        <image
          href={asset()?.url}
          width="256"
          height="256"
          preserveAspectRatio="xMidYMid meet"
          onError={() => setFailed(asset()?.id ?? "")}
        />
      </svg>
    </Show>
  )
}
export const SiteFolderIcon = (props: IconProps) => (
  <AssetIcon
    {...props}
    asset={siteIconConfig().selections.folder}
    fallback={IoFolder}
  />
)
export const SiteDownloadIcon = (props: IconProps) => (
  <AssetIcon
    {...props}
    asset={siteIconConfig().selections.download}
    fallback={AiOutlineCloudDownload}
  />
)
