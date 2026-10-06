import { Button, HStack, Text, VStack } from "@hope-ui/solid"
import { Show, type JSXElement } from "solid-js"
import { useRouter } from "~/hooks"
import { getDirectoryReturnHref } from "~/store/history"
import {
  directoryBusy,
  type DirectoryFailure,
} from "~/store/directory-navigation"
import { pathDir } from "~/utils"
import { uxText } from "~/utils/ux"

export const DirectoryError = (props: {
  failure?: DirectoryFailure
  retry: () => unknown
  compact?: boolean
  actions?: JSXElement
}) => {
  const { pathname, to, isShare } = useRouter()
  const kind = () => props.failure?.kind ?? "other"
  const permission = () => kind() === "permission" || kind() === "password"
  const message = () => {
    if (kind() === "missing")
      return uxText(
        "这个目录已不存在或已移动。",
        "This directory was removed or moved.",
        "這個目錄已不存在或已移動。",
      )
    if (permission())
      return uxText(
        "暂时无法访问此目录，请检查账号或目录密码。",
        "This directory is not accessible. Check your account or directory password.",
        "暫時無法存取此目錄，請檢查帳號或目錄密碼。",
      )
    if (props.compact)
      return props.failure?.refresh
        ? uxText(
            "刷新未完成，已保留当前列表。",
            "Refresh did not finish. Your current list has been kept.",
            "重新整理未完成，已保留目前列表。",
          )
        : uxText(
            "未能加载下一页，已加载的文件仍可使用。",
            "Could not load the next page. Loaded files are still available.",
            "未能載入下一頁，已載入的檔案仍可使用。",
          )
    if (kind() === "network")
      return uxText(
        "连接暂时不可用，请检查网络后重试。",
        "The connection is unavailable. Check your network and try again.",
        "連線暫時無法使用，請檢查網路後重試。",
      )
    return uxText(
      "暂时无法加载目录，请稍后重试。",
      "The directory could not be loaded. Please try again later.",
      "暫時無法載入目錄，請稍後重試。",
    )
  }
  const parent = () => pathDir(pathname()) || "/"
  const canGoUp = () =>
    pathname() !== "/" &&
    (!isShare() || pathname().split("/").filter(Boolean).length > 2)
  return (
    <VStack
      class="directory-error"
      role="status"
      w="$full"
      p={props.compact ? "$3" : "$6"}
      spacing="$3"
    >
      <Text textAlign="center">{message()}</Text>
      <HStack spacing="$2" flexWrap="wrap" justifyContent="center">
        <Show when={!permission()}>
          <Button disabled={directoryBusy()} onClick={() => props.retry()}>
            {uxText("重试", "Retry", "重試")}
          </Button>
        </Show>
        <Show when={permission()}>
          <Button
            onClick={() =>
              to(`/@login?redirect=${encodeURIComponent(location.pathname)}`)
            }
          >
            {uxText(
              "登录或切换账号",
              "Sign in or switch account",
              "登入或切換帳號",
            )}
          </Button>
        </Show>
        <Show when={canGoUp()}>
          <Button
            variant="outline"
            onClick={() =>
              to(getDirectoryReturnHref(parent()), false, {
                preserveHistory: true,
              })
            }
          >
            {uxText("返回上级", "Parent directory", "返回上層")}
          </Button>
        </Show>
        {props.actions}
      </HStack>
      <Show when={props.failure?.details}>
        <details class="directory-error-details">
          <summary>
            {uxText("技术详情", "Technical details", "技術詳情")}
          </summary>
          <code>{props.failure?.details}</code>
        </details>
      </Show>
    </VStack>
  )
}
