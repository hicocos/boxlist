import {
  Badge,
  Button,
  HStack,
  Icon,
  IconButton,
  Input,
  Modal,
  ModalBody,
  ModalCloseButton,
  ModalContent,
  ModalHeader,
  ModalOverlay,
  Select,
  SelectContent,
  SelectIcon,
  SelectListbox,
  SelectOption,
  SelectOptionIndicator,
  SelectOptionText,
  SelectTrigger,
  SelectValue,
  Text,
  VStack,
  hope,
} from "@hope-ui/solid"
import { BsSearch } from "solid-icons/bs"
import {
  createMemo,
  createSignal,
  For,
  Match,
  onCleanup,
  onMount,
  Show,
  Switch,
} from "solid-js"
import Mark from "mark.js"
import { FullLoading, LinkWithBase, Paginator } from "~/components"
import { usePath, useRouter, useT } from "~/hooks"
import { getMainColor, me, password } from "~/store"
import { bus, getFileSize, hoverColor, r } from "~/utils"
import { isMac } from "~/utils/compatibility"
import { getIconByObj } from "~/utils/icon"
import { uxText } from "~/utils/ux"
import {
  canCloseSearchKey,
  canSubmitSearchKey,
  createSearchController,
  decodeSearchPage,
  emptySearchState,
  queryFromDraft,
  sameQuery,
  searchAccountKey,
  searchSessions,
  SEARCH_PAGE_SIZE,
  type SearchEntry,
  type SearchQuery,
  type SearchScope,
} from "./search-state"
import "./search.css"

function NodeName(props: { keywords: string; name: string }) {
  let ref!: HTMLSpanElement
  onMount(() => {
    new Mark(ref).mark(props.keywords, {
      separateWordSearch: true,
      diacritics: true,
    })
  })
  return (
    <hope.span
      ref={ref}
      css={{
        mark: {
          bg: "$info4",
          rounded: "$md",
          px: "1px",
          color: "$info11",
          fontWeight: "$bold",
        },
      }}
    >
      {props.name}
    </hope.span>
  )
}

const SearchResult = (props: {
  node: SearchEntry
  keywords: string
  onNavigate: (event: MouseEvent, path: string) => void
}) => {
  const { setPathAs } = usePath()
  const rememberType = () =>
    setPathAs(decodeURIComponent(props.node.path), props.node.is_dir)
  return (
    <HStack
      class="search-result"
      data-search-path={props.node.path}
      w="$full"
      borderBottom={`1px solid ${hoverColor()}`}
      _hover={{ bgColor: hoverColor() }}
      rounded="$md"
      px="$2"
      as={LinkWithBase}
      href={`${props.node.path}?from=search`}
      onMouseEnter={rememberType}
      onFocus={rememberType}
      onClick={(event: MouseEvent) => {
        rememberType()
        props.onNavigate(event, props.node.path)
      }}
    >
      <Icon
        class="icon"
        boxSize="$6"
        flexShrink={0}
        color={getMainColor()}
        as={getIconByObj(props.node)}
        mr="$1"
      />
      <VStack
        flex={1}
        minW={0}
        p="$1"
        spacing="$1"
        w="$full"
        alignItems="start"
      >
        <Text css={{ overflowWrap: "anywhere" }}>
          <NodeName keywords={props.keywords} name={props.node.name} />
          <Show when={props.node.size > 0 || !props.node.is_dir}>
            <Badge colorScheme="info" ml="$2">
              {getFileSize(props.node.size)}
            </Badge>
          </Show>
        </Text>
        <Text color="$neutral11" size="xs" css={{ overflowWrap: "anywhere" }}>
          {props.node.parent}
        </Text>
      </VStack>
    </HStack>
  )
}

const SearchSession = (props: { directory: string; identity: string }) => {
  const t = useT()
  searchSessions.account(props.identity)
  const initial =
    searchSessions.get(props.identity, props.directory) ?? emptySearchState()
  const [state, setState] = createSignal(initial)
  let body: HTMLDivElement | undefined

  let composing = false
  let leaving = false
  let returnFocus: HTMLElement | undefined
  let frame = 0
  let focusTimer: ReturnType<typeof setTimeout> | undefined
  const controller = createSearchController(
    initial,
    async (query, page, signal) => {
      // Use the existing authenticated client, but supply a cancellable search-only request.
      const response = await r.post(
        "/fs/search",
        {
          parent: "/",
          keywords: query.keywords,
          scope: query.scope,
          page,
          per_page: SEARCH_PAGE_SIZE,
          password: password(),
        },
        { signal },
      )
      return decodeSearchPage(response, me().base_path ?? "/")
    },
    (next) => {
      const newResult = next.result !== state().result
      setState(next)
      if (newResult && next.status === "success") {
        queueMicrotask(() => {
          if (body && state().result === next.result) body.scrollTop = 0
        })
      }
      if (!leaving && searchAccountKey(me()) === props.identity) {
        searchSessions.put(props.identity, props.directory, next)
      }
    },
  )
  const draftQuery = createMemo(() =>
    queryFromDraft(state().draft, props.directory),
  )
  const loading = () => state().status === "loading"
  const retained = () => {
    const current = state()
    return (
      !!current.result &&
      (current.status !== "success" ||
        !sameQuery(draftQuery(), current.result.query) ||
        !sameQuery(current.submitted, current.result.query) ||
        current.requestedPage !== current.result.page)
    )
  }
  const scopeLabel = (scope: SearchScope) =>
    [
      uxText("全部", "All", "全部"),
      uxText("文件夹", "Folders", "資料夾"),
      uxText("文件", "Files", "檔案"),
    ][scope]
  const describe = (query: SearchQuery) =>
    `“${query.keywords}” · ${scopeLabel(query.scope)}`
  const errorText = () => {
    switch (state().error) {
      case "timeout":
        return uxText(
          "搜索超时，请重试。",
          "Search timed out. Please retry.",
          "搜尋逾時，請重試。",
        )
      case "access":
        return uxText(
          "没有搜索权限或目录密码已失效，请检查登录和目录访问权限。",
          "Search access was denied. Check your login and directory access.",
          "沒有搜尋權限或目錄密碼已失效，請檢查登入和目錄存取權限。",
        )
      case "invalid":
        return uxText(
          "搜索返回的数据不完整，请重试。",
          "The search response was incomplete. Please retry.",
          "搜尋傳回的資料不完整，請重試。",
        )
      case "server":
        return uxText(
          "服务端未能完成搜索，请稍后重试。",
          "The server could not complete this search. Please retry.",
          "伺服器未能完成搜尋，請稍後重試。",
        )
      default:
        return uxText(
          "无法连接搜索服务，请检查网络后重试。",
          "Could not reach search. Check your connection and retry.",
          "無法連線搜尋服務，請檢查網路後重試。",
        )
    }
  }
  const restoreView = () => {
    cancelAnimationFrame(frame)
    frame = requestAnimationFrame(() => {
      frame = requestAnimationFrame(() => {
        if (!state().open || !body) return
        const top = state().scrollTop
        const path = state().focusPath
        if (path) {
          Array.from(body.querySelectorAll<HTMLElement>("[data-search-path]"))
            .find((row) => row.dataset.searchPath === path)
            ?.focus({ preventScroll: true })
        }
        body.scrollTop = top
      })
    })
  }
  const rememberScroll = () => {
    if (body) controller.setView({ scrollTop: body.scrollTop })
  }
  const open = () => {
    if (state().open) return
    clearTimeout(focusTimer)
    returnFocus =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : undefined
    leaving = false
    controller.setView({ open: true })
    restoreView()
  }
  const close = () => {
    rememberScroll()
    controller.cancel()
    controller.setView({ open: false })
    // Hope also restores focus; explicitly retain the shortcut/menu trigger when available.
    focusTimer = setTimeout(() => {
      if (!state().open && returnFocus?.isConnected)
        returnFocus.focus({ preventScroll: true })
    }, 0)
  }
  const submit = () => {
    if (draftQuery().keywords) void controller.submit(props.directory)
  }
  const changePage = (page: number) => {
    void controller.page(page)
  }
  const beforeNavigate = (event: MouseEvent, path: string) => {
    if (
      event.defaultPrevented ||
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    )
      return
    rememberScroll()
    controller.setView({ focusPath: path })
    controller.cancel()
    // Keep the source directory's open state for browser Back/return-to-directory.
    searchSessions.put(props.identity, props.directory, {
      ...controller.getState(),
      open: true,
    })
    leaving = true
    controller.setView({ open: false })
  }
  const handler = (name: string) => {
    if (name === "search") open()
  }
  const searchEvent = (event: KeyboardEvent) => {
    if (event.isComposing || composing || event.keyCode === 229) return
    if (
      (event.ctrlKey || (isMac && event.metaKey)) &&
      event.key.toLowerCase() === "k"
    ) {
      event.preventDefault()
      event.stopPropagation()
      state().open ? close() : open()
      return
    }
    if (!state().open) return
    // Capture before Hope closes its select. The same Escape must not close both layers.
    // This Hope version consumes ModalContent.ref without forwarding it.
    // ModalBody's actual node is reliable, including in a portaled dialog.
    const menu = body
      ?.closest(".search-dialog")
      ?.querySelector<HTMLButtonElement>(
        '.hope-select__trigger[aria-expanded="true"]',
      )
    if (event.key === "Escape" && menu) {
      // Hope's focus-trap otherwise deactivates on Escape even while a menu is open.
      event.preventDefault()
      event.stopImmediatePropagation()
      menu.click()
      menu.focus({ preventScroll: true })
      return
    }
    if (canCloseSearchKey(event, !!menu)) {
      event.preventDefault()
      event.stopImmediatePropagation()
      close()
    }
  }
  bus.on("tool", handler)
  document.addEventListener("keydown", searchEvent, true)
  onMount(() => {
    if (initial.open) restoreView()
  })
  onCleanup(() => {
    cancelAnimationFrame(frame)
    clearTimeout(focusTimer)
    if (!leaving && body) controller.setView({ scrollTop: body.scrollTop })
    controller.dispose()
    bus.off("tool", handler)
    document.removeEventListener("keydown", searchEvent, true)
  })

  return (
    <Modal
      opened={state().open}
      onClose={close}
      closeOnEsc={false}
      motionPreset="none"
      size={{ "@initial": "sm", "@sm": "lg", "@md": "2xl" }}
      initialFocus="#search-input"
      scrollBehavior="inside"
    >
      <ModalOverlay bg="$blackAlpha5" />
      <ModalContent class="search-dialog" mx="$2">
        <ModalCloseButton
          aria-label={uxText("关闭搜索", "Close search", "關閉搜尋")}
        />
        <ModalHeader>{t("home.search.search")}</ModalHeader>
        <ModalBody
          ref={(element: HTMLDivElement) => {
            body = element
            restoreView()
          }}
          onScroll={rememberScroll}
        >
          <VStack w="$full" spacing="$2">
            <HStack
              class="search-controls"
              w="$full"
              spacing="$2"
              css={{
                ".hope-select__trigger": {
                  flexShrink: 0,
                  minWidth: 0,
                  px: "$2",
                },
                ".hope-select__value": {
                  flex: "0 0 auto",
                  whiteSpace: "nowrap",
                },
              }}
            >
              <Select
                value={state().draft.scope}
                onChange={(value: SearchScope) =>
                  controller.setDraft({ scope: value })
                }
              >
                <SelectTrigger
                  w="max-content"
                  aria-label={uxText("文件类型", "File type", "檔案類型")}
                >
                  <SelectValue />
                  <SelectIcon />
                </SelectTrigger>
                <SelectContent
                  class="search-scope-options"
                  minW="max-content"
                  css={{
                    ".hope-select__option-text": {
                      whiteSpace: "nowrap",
                      padding: "$2 $8 $2 $2",
                    },
                  }}
                >
                  <SelectListbox>
                    <For each={[0, 1, 2] as SearchScope[]}>
                      {(scope) => (
                        <SelectOption value={scope}>
                          <SelectOptionText>
                            {scopeLabel(scope)}
                          </SelectOptionText>
                          <SelectOptionIndicator />
                        </SelectOption>
                      )}
                    </For>
                  </SelectListbox>
                </SelectContent>
              </Select>
              <Input
                id="search-input"
                flex="1 1 0%"
                minW={0}
                w={0}
                value={state().draft.keywords}
                maxLength={512}
                aria-label={uxText(
                  "搜索关键词",
                  "Search keywords",
                  "搜尋關鍵字",
                )}
                autocomplete="off"
                onInput={(event) =>
                  controller.setDraft({ keywords: event.currentTarget.value })
                }
                onCompositionStart={() => {
                  composing = true
                }}
                onCompositionEnd={() => {
                  composing = false
                }}
                onKeyDown={(event) => {
                  if (canSubmitSearchKey(event, composing)) {
                    event.preventDefault()
                    submit()
                  }
                }}
              />
              <IconButton
                flexShrink={0}
                aria-label={t("home.search.search")}
                icon={<BsSearch />}
                onClick={submit}
                disabled={!draftQuery().keywords}
              />
            </HStack>
            <Switch>
              <Match when={loading()}>
                <FullLoading />
              </Match>
              <Match
                when={
                  state().status === "idle" ||
                  (state().status === "success" &&
                    state().result?.content.length === 0)
                }
              >
                <Text size="2xl" my="$8" role="status">
                  {t("home.search.no_result")}
                </Text>
              </Match>
              <Match when={state().status === "error"}>
                <Text class="search-error" role="alert">
                  {errorText()}
                </Text>
              </Match>
              <Match when={state().status === "cancelled"}>
                <Text size="sm" role="status">
                  {uxText("搜索已取消", "Search cancelled", "搜尋已取消")}
                </Text>
              </Match>
            </Switch>
            <Show when={loading()}>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => controller.cancel()}
              >
                {uxText("取消", "Cancel", "取消")}
              </Button>
            </Show>
            <Show
              when={
                state().status === "error" || state().status === "cancelled"
              }
            >
              <Button size="sm" onClick={() => void controller.retry()}>
                {uxText("重试", "Retry", "重試")}
              </Button>
            </Show>
            <Show when={retained() && state().result!.content.length > 0}>
              <Text
                class="search-retained"
                size="xs"
                color="$neutral11"
                role="status"
              >
                {uxText(
                  "仍显示上次结果：",
                  "Previous results: ",
                  "仍顯示上次結果：",
                )}
                {describe(state().result!.query)}
              </Text>
            </Show>
            <VStack w="$full" aria-busy={loading()}>
              <For each={state().result?.content ?? []}>
                {(item) => (
                  <SearchResult
                    node={item}
                    keywords={state().result!.query.keywords}
                    onNavigate={beforeNavigate}
                  />
                )}
              </For>
            </VStack>
            <div class="search-pagination">
              <Paginator
                total={state().result?.total ?? 0}
                defaultPageSize={SEARCH_PAGE_SIZE}
                current={state().result?.page ?? 1}
                disabled={loading()}
                onChange={changePage}
              />
            </div>
          </VStack>
        </ModalBody>
      </ModalContent>
    </Modal>
  )
}

const Search = () => {
  const { pathname } = useRouter()
  const session = createMemo(() =>
    JSON.stringify([searchAccountKey(me()), pathname()]),
  )
  return (
    <Show when={session()} keyed>
      {(key) => {
        const [identity, directory] = JSON.parse(key) as [string, string]
        return <SearchSession identity={identity} directory={directory} />
      }}
    </Show>
  )
}

export { Search }
