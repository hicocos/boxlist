import { Button, Icon, IconButton, Tooltip, VStack } from "@hope-ui/solid"
import { createMemo, createResource, createSignal, ErrorBoundary, Show, Suspense, onMount } from "solid-js"
import { Dynamic } from "solid-js/web"
import { clsx } from "clsx"
import { FiDownload, FiLink } from "solid-icons/fi"
import { Error, FullLoading, SelectWrapper } from "~/components"
import { objStore } from "~/store"
import { useCopyLink, useRouter, useT } from "~/hooks"
import { Download } from "../previews/download"
import { OpenWith } from "./open-with"
import { getPreviews } from "../previews"
import { loadArchiveExtensions } from "~/store/archive"
import { markPrimaryReady } from "~/app/primary-ready"
import { getFileSize } from "~/utils"
import { FileToolbarSlotContext } from "./toolbar-slot"
import "./file-toolbar.css"

const File = () => {
  onMount(markPrimaryReady)
  const [archiveError, setArchiveError] = createSignal(false)
  const [archives, { refetch }] = createResource(async () => {
    setArchiveError(false)
    try { return await loadArchiveExtensions() }
    catch { setArchiveError(true); return false }
  })
  return <Show when={!archiveError()} fallback={<div role="status">预览配置加载失败。<button onClick={() => refetch()}>重试</button></div>}>
    <Suspense fallback={<FullLoading />}><Show when={archives()}><FileContent /></Show></Suspense>
  </Show>
}

const FileContent = () => {
  const t = useT()
  const { searchParams, setSearchParams } = useRouter()
  const { copyCurrentRawLink } = useCopyLink()
  const previews = getPreviews({ ...objStore.obj, provider: objStore.provider })
  const cur = createMemo(() => {
    if (!previews.length) return undefined
    const selected = searchParams["preview"]
    if (selected) {
      const found = previews.find((item) => item.key === selected)
      if (found) return found
    }
    return previews[0]
  })
  const isDownloadView = () => cur()?.key === "download"
  const [extraMount, setExtraMount] = createSignal<HTMLDivElement>()
  const [hasExtras, setHasExtras] = createSignal(false)

  return (
    <Show when={previews.length > 1} fallback={<Download openWith />}>
      <FileToolbarSlotContext.Provider
        value={{ mount: extraMount, hasExtras, setHasExtras }}
      >
        <VStack w="$full" spacing="$2">
          <div class={clsx("file-preview-bar", hasExtras() && "has-extras")}>
            <div class="file-preview-select">
              <SelectWrapper
                alwaysShowBorder
                size="sm"
                value={cur()?.key || ""}
                onChange={(key) => {
                  setSearchParams({ preview: key }, { replace: true })
                }}
                options={previews.map((item) => ({
                  value: item.key,
                  label: item.name,
                }))}
              />
            </div>
            <div class="file-preview-actions">
              <div
                class="file-preview-extra-slot"
                ref={(el) => setExtraMount(el)}
              />
              <Show when={!isDownloadView()}>
                <div class="file-preview-primary-actions">
                  <Tooltip label={t("home.toolbar.copy_link")} withArrow>
                    <IconButton
                      type="button"
                      size="sm"
                      colorScheme="neutral"
                      data-glass-cta="secondary"
                      aria-label={t("home.toolbar.copy_link")}
                      icon={<Icon as={FiLink} />}
                      onClick={() => copyCurrentRawLink(true)}
                    />
                  </Tooltip>
                  <Button
                    as="a"
                    href={objStore.raw_url}
                    target="_blank"
                    rel="noopener noreferrer"
                    size="sm"
                    colorScheme="info"
                    data-glass-cta="primary"
                    leftIcon={<Icon as={FiDownload} />}
                    title={`${t("home.preview.download")} (${getFileSize(objStore.obj.size)})`}
                  >
                    {t("home.preview.download")}
                  </Button>
                </div>
              </Show>
              <OpenWith size="sm" />
            </div>
          </div>
          <ErrorBoundary fallback={(err) => <Error msg={String(err)} />}>
            <Suspense fallback={<FullLoading />}>
              <Dynamic component={cur()?.component} />
            </Suspense>
          </ErrorBoundary>
        </VStack>
      </FileToolbarSlotContext.Provider>
    </Show>
  )
}

export default File
