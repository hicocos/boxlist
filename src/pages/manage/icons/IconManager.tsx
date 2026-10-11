import { iconFormatLabel } from "./icon-format"
import { TransparentIconAction, iconTransparencyBusy, disposeTransparencyWatchers } from "./TransparentIconAction"
import {
  createMemo,
  createSignal,
  For,
  onCleanup,
  onMount,
  Show,
} from "solid-js"
import { Portal } from "solid-js/web"
import { useManageTitle } from "~/hooks"
import { useBeforeLeave, useLocation } from "@solidjs/router"
import { registerIconLeaveGuard } from "~/utils/icon-navigation-guard"
import { me } from "~/store"
import { UserMethods } from "~/types"
import { AssetIcon } from "~/components/SiteIcon"
import {
  allIconAssets,
  iconAssetById,
  removeLibraryIcon,
} from "~/utils/icon-assets"
import {
  type IconConfig,
  type IconTarget,
  ICON_TARGETS,
  DEFAULT_ICON,
  canonicalIconConfig,
  defaultIconConfig,
  previewIconAsset,
  changedIconTargetIds,
  reconcileIconDraft,
  filterIconManagerTargets,
  iconCanInherit,
  iconSelectionValue,
  selectIconForTarget,
} from "~/utils/icon-policy"
import { getDefaultIconForTarget } from "~/utils/icon"
import { uxText } from "~/utils/ux"
import { loadIconSettings, saveIconSettings } from "./icon-settings"
import {
  IconUploader,
  iconLibraryAdapter,
  type IconLibraryAdapter,
} from "./IconUploader"
import "./icons.css"

const groups = [
  ["common", "常用类型", "Common types"],
  ["general", "通用分类", "General"],
  ["office", "文档 / 设计", "Documents / design"],
  ["audio", "音频", "Audio"],
  ["video", "视频", "Video"],
  ["image", "图片", "Images"],
  ["archive", "压缩包", "Archives"],
  ["text", "文本 / 代码", "Text / code"],
  ["special", "应用 / 其他", "Apps / other"],
  ["all", "全部类型", "All types"],
]
const label = (target: IconTarget) => uxText(target.label, target.english)
const effectiveAsset = previewIconAsset
const choice = iconSelectionValue

export type IconManagerPanelProps = {
  load: () => Promise<IconConfig>
  save: (draft: IconConfig, baseline: IconConfig) => Promise<IconConfig>
  library?: IconLibraryAdapter
}
/** Same real panel, with explicitly isolated adapters in browser tests. */
export const IconManagerPanel = (props: IconManagerPanelProps) => {
  const [draft, setDraft] = createSignal(defaultIconConfig())
  const [baseline, setBaseline] = createSignal(defaultIconConfig())
  const [ready, setReady] = createSignal(false)
  const [loading, setLoading] = createSignal(false)
  const [saving, setSaving] = createSignal(false)
  const [uploadBusy, setUploadBusy] = createSignal(false)
  const [uploadPending, setUploadPending] = createSignal(false)
  const [libraryDeleting, setLibraryDeleting] = createSignal(false)
  const [catalogLoading, setCatalogLoading] = createSignal(false)
  const [libraryError, setLibraryError] = createSignal("")
  const [libraryNotice, setLibraryNotice] = createSignal("")
  const [libraryMode, setLibraryMode] = createSignal(false)
  const [workspace, setWorkspace] = createSignal("mapping")
  const [libraryPreview, setLibraryPreview] = createSignal("")
  let deleteController: AbortController | undefined
  const [uploadPreviewOpen, setUploadPreviewOpen] = createSignal(false)
  let closeUploadPreview: (() => void) | undefined
  const [uncertain, setUncertain] = createSignal(false)
  const [conflicts, setConflicts] = createSignal<string[]>([])
  const [error, setError] = createSignal("")
  const [status, setStatus] = createSignal("")
  const [group, setGroup] = createSignal("common")
  const [query, setQuery] = createSignal("")
  const [changedOnly, setChangedOnly] = createSignal(false)
  const [selected, setSelected] = createSignal("folder")
  const [pickerOpen, setPickerOpen] = createSignal(false)
  const [pickerSequence, setPickerSequence] = createSignal<IconTarget[]>([])
  const [assetQuery, setAssetQuery] = createSignal("")
  const [assetFormat, setAssetFormat] = createSignal("all")
  const [assetUsageFilter, setAssetUsageFilter] = createSignal("all")
  const [applyQuery, setApplyQuery] = createSignal("")
  const [applyTargets, setApplyTargets] = createSignal<string[]>([])
  const assignmentTargets = createMemo(() => filterIconManagerTargets("all", applyQuery()))
  const libraryAssets = createMemo(() =>
    allIconAssets().filter(
      (asset) =>
        (assetFormat() === "all" || (assetFormat() === "gif") === /\.gif$/i.test(asset.name)) &&
        (assetUsageFilter() === "all" || (assetUsageFilter() === "used") === ICON_TARGETS.some(item =>
          effectiveAsset(draft(), item) === asset.id || effectiveAsset(baseline(), item) === asset.id)) &&
        `${asset.name} ${asset.id}`
          .toLowerCase()
          .includes(assetQuery().trim().toLowerCase()),
    ),
  )
  let active = true
  let generation = 0
  let page!: HTMLElement
  let editor!: HTMLDialogElement
  let pickerBody!: HTMLDivElement
  let shortcuts!: HTMLDetailsElement
  let openedFrom: HTMLButtonElement | undefined
  let focusFrame = 0
  const busy = () => loading() || saving() || libraryDeleting()
  const locked = () => !ready() || busy() || uncertain()
  const canSave = () => !locked() && dirty() && conflicts().length === 0
  const dirty = createMemo(
    () => canonicalIconConfig(draft()) !== canonicalIconConfig(baseline()),
  )
  const changedIds = createMemo(() => changedIconTargetIds(draft(), baseline()))
  const target = createMemo(
    () =>
      ICON_TARGETS.find((item) => item.id === selected()) ?? ICON_TARGETS[0],
  )
  const visibleTargets = createMemo(() =>
    filterIconManagerTargets(
      group(),
      query(),
      changedOnly() ? changedIds() : undefined,
    ),
  )
  const pickerTargets = () => pickerSequence()
  const pickerIndex = () =>
    pickerTargets().findIndex((item) => item.id === selected())
  const visibleAssets = createMemo(() => {
    const chosen = draft().selections[target().id]
    const assets = allIconAssets()
    const selectedAsset = chosen ? iconAssetById(chosen) : undefined
    if (selectedAsset && !assets.some((item) => item.id === selectedAsset.id))
      assets.push(selectedAsset)
    return assets.filter(
      (asset) =>
        `${asset.name} ${asset.id}`
          .toLowerCase()
          .includes(assetQuery().trim().toLowerCase()),
    )
  })
  const effective = () => effectiveAsset(draft(), target())
  const descriptionFor = (config: IconConfig, item: IconTarget) => {
    const name =
      iconAssetById(effectiveAsset(config, item) ?? "")?.name ??
      uxText("默认图标", "Default icon")
    return choice(config, item) === "inherit"
      ? uxText(`跟随通用 · ${name}`, `Inherited · ${name}`)
      : name
  }
  const description = (item: IconTarget) => descriptionFor(draft(), item)
  const saveState = () =>
    uncertain()
      ? uxText("保存结果待核对", "Verification needed")
      : conflicts().length
        ? uxText(
            `${conflicts().length} 项冲突待处理`,
            `${conflicts().length} conflicts to resolve`,
          )
        : error()
          ? uxText("操作失败，可重试", "Failed; retry available")
          : loading()
            ? uxText("正在读取…", "Loading…")
            : dirty()
              ? uxText(
                  `${changedIds().length} 项待保存`,
                  `${changedIds().length} unsaved changes`,
                )
              : ready()
                ? uxText("已保存", "Saved")
                : uxText("尚未加载", "Not loaded")

  const load = async (confirm = false) => {
    if (busy()) return
    if (
      confirm &&
      dirty() &&
      !window.confirm(
        uxText(
          "重新加载会丢弃未保存的选择，继续吗？",
          "Discard unsaved choices and reload?",
        ),
      )
    )
      return
    const id = ++generation
    setLoading(true)
    setError("")
    setStatus("")
    try {
      const config = await props.load()
      if (!active || id !== generation) return
      setDraft(config)
      setBaseline(config)
      setConflicts([])
      setReady(true)
      setUncertain(false)
    } catch (cause) {
      if (active && id === generation)
        setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      if (active && id === generation) setLoading(false)
    }
  }
  onMount(() => {
    void load()
  })
  const closePicker = () => {
    if (libraryDeleting()) return
    const returningToLibrary = libraryMode()
    if (editor.open) editor.close()
    setPickerOpen(false)
    cancelAnimationFrame(focusFrame)
    focusFrame = requestAnimationFrame(() => {
      if (!active || pickerOpen()) return
      const button = Array.from(
        document.querySelectorAll<HTMLButtonElement>(".im-target[data-target]"),
      ).find((item) => item.dataset.target === selected())
      const destination = returningToLibrary
        ? openedFrom?.isConnected
          ? openedFrom
          : undefined
        : (button ?? (openedFrom?.isConnected ? openedFrom : undefined))
      if (destination?.getClientRects().length) destination.focus({ preventScroll: true })
      else page.focus({ preventScroll: true })
    })
  }
  const trapPickerFocus = (event: KeyboardEvent) => {
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
      event.preventDefault()
      if (canSave()) void save()
      return
    }
    if (event.key !== "Tab" || event.ctrlKey || event.metaKey || event.altKey)
      return
    const tabbables = Array.from(
      editor.querySelectorAll<HTMLElement>(
        'button:not(:disabled), input:not(:disabled), select:not(:disabled), a[href], [tabindex]:not([tabindex="-1"])',
      ),
    ).filter((element) => {
      if (
        element.tabIndex < 0 ||
        element.matches(":disabled") ||
        !element.getClientRects().length
      )
        return false
      if (element instanceof HTMLInputElement && element.type === "radio") {
        const peers = Array.from(
          editor.querySelectorAll<HTMLInputElement>('input[type="radio"]'),
        ).filter(
          (peer) =>
            peer.name === element.name &&
            !peer.matches(":disabled") &&
            peer.getClientRects().length,
        )
        return element === (peers.find((peer) => peer.checked) ?? peers[0])
      }
      return true
    })
    const first = tabbables[0],
      last = tabbables.at(-1)
    if (!first || !last) {
      event.preventDefault()
      editor.focus({ preventScroll: true })
      return
    }
    if (
      event.shiftKey &&
      (document.activeElement === first || document.activeElement === editor)
    ) {
      event.preventDefault()
      last.focus({ preventScroll: true })
    } else if (
      !event.shiftKey &&
      (document.activeElement === last || document.activeElement === editor)
    ) {
      event.preventDefault()
      first.focus({ preventScroll: true })
    }
  }
  let approvedNavigation = ""
  const unfinishedUploads = () => uploadBusy() || uploadPending()
  const approveLeave = () => {
    if (!dirty() && !unfinishedUploads()) return true
    const now = performance.now()
    if (approvedNavigation && now - Number(approvedNavigation) < 500)
      return true
    const approved = window.confirm(
      uxText(
        unfinishedUploads()
          ? "离开会丢失未完成上传任务、重试标识及未保存选择；未核对的素材可能已保存，建议先用原任务重试。继续吗？"
          : "离开会丢弃未保存的图标选择，继续吗？",
        unfinishedUploads()
          ? "Leave and lose unfinished upload tasks, retry IDs and unsaved choices? Unverified uploads may already be saved; retry the original tasks first."
          : "Leave and discard unsaved icon choices?",
      ),
    )
    if (approved) approvedNavigation = String(now)
    return approved
  }
  useBeforeLeave((event) => {
    if (event.defaultPrevented) return
    if (typeof event.to === "number" && (pickerOpen() || uploadPreviewOpen())) {
      if (pickerOpen()) closePicker()
      else closeUploadPreview?.()
      event.preventDefault()
      return
    }
    if (!approveLeave()) event.preventDefault()
  })
  const routeLocation = useLocation()
  const boundUrl = new URL(
    routeLocation.pathname + routeLocation.search + routeLocation.hash,
    location.origin,
  ).href
  const removeHistoryGuard = registerIconLeaveGuard({
    href: boundUrl,
    state: routeLocation.state,
    allow: () => {
      if (pickerOpen() || uploadPreviewOpen()) {
        if (pickerOpen()) closePicker()
        else closeUploadPreview?.()
        return false
      }
      return approveLeave()
    },
  })
  const beforeUnload = (event: BeforeUnloadEvent) => {
    if (dirty() || unfinishedUploads()) {
      event.preventDefault()
      event.returnValue = ""
    }
  }
  const beforeNavigate = (event: MouseEvent) => {
    if (
      (!dirty() && !unfinishedUploads()) ||
      event.defaultPrevented ||
      event.button !== 0 ||
      event.ctrlKey ||
      event.metaKey ||
      event.altKey ||
      event.shiftKey
    )
      return
    const anchor = (event.target as Element | null)?.closest?.(
      "a[href]",
    ) as HTMLAnchorElement | null
    if (
      !anchor ||
      anchor.target === "_blank" ||
      anchor.hasAttribute("download")
    )
      return
    const next = new URL(anchor.href, location.href)
    if (
      next.href === location.href ||
      (next.origin === location.origin &&
        next.pathname === location.pathname &&
        next.search === location.search)
    )
      return
    if (!approveLeave()) {
      event.preventDefault()
      event.stopImmediatePropagation()
    }
  }
  const closeShortcutsOutside = (event: PointerEvent) => {
    if (shortcuts?.open && !shortcuts.contains(event.target as Node))
      shortcuts.open = false
  }
  window.addEventListener("beforeunload", beforeUnload)
  document.addEventListener("click", beforeNavigate, true)
  document.addEventListener("pointerdown", closeShortcutsOutside)
  onCleanup(() => {
    active = false
    ++generation
    deleteController?.abort()
    disposeTransparencyWatchers()
    cancelAnimationFrame(focusFrame)
    if (editor?.open) editor.close()
    removeHistoryGuard()
    window.removeEventListener("beforeunload", beforeUnload)
    document.removeEventListener("click", beforeNavigate, true)
    document.removeEventListener("pointerdown", closeShortcutsOutside)
  })
  const select = (value: string) => {
    if (locked()) return
    setDraft((old) => selectIconForTarget(old, target(), value))
    setStatus("")
  }
  const openTarget = (id: string, from?: HTMLButtonElement) => {
    setLibraryMode(false)
    setLibraryError("")
    setLibraryNotice("")
    setSelected(id)
    setAssetQuery("")
    if (from) openedFrom = from
    shortcuts.open = false
    cancelAnimationFrame(focusFrame)
    if (!editor.open) {
      const sequence = conflicts().includes(id)
        ? ICON_TARGETS.filter((item) => conflicts().includes(item.id))
        : visibleTargets()
      setPickerSequence(
        sequence.some((item) => item.id === id) ? [...sequence] : [target()],
      )
      editor.showModal()
    } else if (!pickerTargets().some((item) => item.id === id)) {
      setPickerSequence([target()])
    }
    setPickerOpen(true)
    pickerBody.scrollTop = 0
    // Focus stays in the top-layer dialog; the type grid never auto-scrolls.
    editor.focus({ preventScroll: true })
  }
  const openLibrary = (from: HTMLButtonElement, id: string) => {
    if (uploadBusy() || busy() || catalogLoading()) return
    openedFrom = from
    cancelAnimationFrame(focusFrame)
    setLibraryMode(true)
    setLibraryPreview(id)
    setApplyQuery("")
    setApplyTargets([])
    setLibraryError("")
    setLibraryNotice("")
    setPickerSequence([target()])
    shortcuts.open = false
    if (!editor.open) editor.showModal()
    setPickerOpen(true)
    pickerBody.scrollTop = 0
    editor.focus({ preventScroll: true })
  }
  const assetUsage = (id: string) =>
    ICON_TARGETS.filter(
      (item) => effectiveAsset(draft(), item) === id || effectiveAsset(baseline(), item) === id,
    )
  const applyAsset = () => {
    const id = libraryPreview()
    if (locked() || conflicts().length || !iconAssetById(id) || !applyTargets().length) return
    setDraft(old => ({ version: 1, selections: { ...old.selections, ...Object.fromEntries(applyTargets().map(target => [target, id])) } }))
    setStatus(uxText(`已为 ${applyTargets().length} 项类型选择素材，请保存更改。`, `Selected for ${applyTargets().length} types. Save to apply.`))
    closePicker()
    setWorkspace("mapping")
    setChangedOnly(true)
    setQuery("")
    setGroup("all")
  }
  const deleteAsset = async (id: string) => {
    if (locked() || uploadBusy() || catalogLoading() || iconTransparencyBusy(id)) return
    const asset = iconAssetById(id)
    if (!asset) return
    const used = assetUsage(id)
    if (used.length) {
      setLibraryError(
        uxText(
          `正在使用：${used.map(label).join("、")}。请先更换并保存后再删除。`,
          `In use: ${used.map(label).join(", ")}. Replace and save first.`,
        ),
      )
      return
    }
    if (
      !window.confirm(
        uxText(
          `删除素材“${asset.name}”？会从素材库移除，不会修改文件目录或其他素材。`,
          `Remove "${asset.name}" from the icon library? Other assets and files are unaffected.`,
        ),
      )
    )
      return
    const controller = new AbortController()
    deleteController = controller
    setLibraryDeleting(true)
    setLibraryError("")
    setLibraryNotice("")
    try {
      await (props.library ?? iconLibraryAdapter).remove(id, controller.signal)
      if (!active || controller.signal.aborted) return
      removeLibraryIcon(id)
      if (editor.open) editor.focus({ preventScroll: true })
      else page.focus({ preventScroll: true })
      setLibraryNotice(
        uxText(`已删除素材：${asset.name}`, `Removed: ${asset.name}`),
      )
    } catch (cause) {
      if (active && !controller.signal.aborted)
        setLibraryError(
          cause instanceof Error ? cause.message : "删除失败，请重试",
        )
    } finally {
      if (active) {
        setLibraryDeleting(false)
        deleteController = undefined
      }
    }
  }
  const adjacent = (direction: number) => {
    const next = pickerTargets()[pickerIndex() + direction]
    if (next) openTarget(next.id)
  }
  const resolveConflict = (id: string, keepLocal: boolean) => {
    if (locked() || !conflicts().includes(id)) return
    if (!keepLocal)
      setDraft((old) => {
        const selections = { ...old.selections }
        const saved = baseline().selections[id]
        if (saved === undefined) delete selections[id]
        else selections[id] = saved
        return { version: 1, selections }
      })
    const remaining = conflicts().filter((item) => item !== id)
    setConflicts(remaining)
    setStatus("")
    if (remaining.length) openTarget(remaining[0])
    else editor.focus({ preventScroll: true })
  }
  const reviewConflicts = () => {
    const id = conflicts()[0]
    if (id) openTarget(id)
  }
  const quick = (action: "defaults" | "reload") => {
    shortcuts.open = false
    if (action === "reload") {
      void load(true)
      return
    }
    if (locked() || conflicts().length) return
    if (
      !window.confirm(
        uxText(
          "将全部类型改为默认图标？点击保存后才会生效。",
          "Use default icons for all types? Changes apply only after saving.",
        ),
      )
    )
      return
    setDraft(defaultIconConfig())
    setStatus("")
    setError("")
  }
  const save = async () => {
    if (!canSave()) return
    const id = ++generation,
      snapshot = draft()
    setSaving(true)
    setError("")
    setStatus("")
    try {
      const config = await props.save(snapshot, baseline())
      if (!active || id !== generation) return
      setDraft(config)
      setBaseline(config)
      setConflicts([])
      setUncertain(false)
      setStatus(
        uxText(
          "已保存并核对。刷新站点即可看到新图标。",
          "Saved and verified. Refresh the site to see the new icons.",
        ),
      )
    } catch (cause) {
      if (active && id === generation) {
        if (
          cause instanceof Error &&
          ["IconSaveUncertain", "IconConfigConflict"].includes(cause.name)
        )
          setUncertain(true)
        setError(cause instanceof Error ? cause.message : String(cause))
      }
    } finally {
      if (active && id === generation) setSaving(false)
    }
  }
  const reconcile = async () => {
    if (busy()) return
    const id = ++generation
    setLoading(true)
    setError("")
    setStatus("")
    try {
      const saved = await props.load()
      if (!active || id !== generation) return
      const result = reconcileIconDraft(draft(), baseline(), saved)
      const same =
        canonicalIconConfig(saved) === canonicalIconConfig(result.draft)
      setDraft(result.draft)
      setBaseline(saved)
      setConflicts(result.conflicts)
      setReady(true)
      setUncertain(false)
      setStatus(
        result.conflicts.length
          ? uxText(
              "同一类型被另一页修改，请处理冲突后保存；其他类型的改动已合并。",
              "Another page changed the same types. Resolve conflicts before saving; other changes were merged.",
            )
          : same
            ? uxText(
                "已核对：当前选择已经保存。",
                "Verified: current choices were saved.",
              )
            : uxText(
                "已合并服务器的新改动，保留本页未保存选择。请检查后再保存。",
                "Server changes merged; your unsaved choices are retained. Review before saving.",
              ),
      )
    } catch (cause) {
      if (active && id === generation)
        setError(cause instanceof Error ? cause.message : String(cause))
    } finally {
      if (active && id === generation) setLoading(false)
    }
  }

  return (
    <section
      ref={page}
      tabIndex={-1}
      class="icon-manager im-page"
      aria-busy={busy()}
      onKeyDown={event => {
        if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
          event.preventDefault()
          if (canSave()) void save()
        }
      }}
    >
      <header class="im-heading">
        <div>
          <h1>{uxText("图标管理", "Icon manager")}</h1>
          <p>
            {uxText(
              "先上传素材，再为文件类型选用。保存后全站生效。",
              "Upload assets, choose them for file types, then save to apply site-wide.",
            )}
          </p>
        </div>
        <small>
          {allIconAssets().length} {uxText("款素材", "assets")} ·{" "}
          {ICON_TARGETS.length} {uxText("项类型", "types")}
        </small>
      </header>
      <nav
        class="im-workspaces"
        aria-label={uxText("图标工作区", "Icon workspace")}
      >
        <button
          type="button"
          aria-pressed={workspace() === "mapping"}
          onClick={() => setWorkspace("mapping")}
        >
          {uxText("图标配置", "Icon settings")}{" "}
          <span>{ICON_TARGETS.length}</span>
        </button>
        <button
          type="button"
          aria-pressed={workspace() === "library"}
          onClick={() => {
            setWorkspace("library")
          }}
        >
          {uxText("素材库", "Asset library")}{" "}
          <span>{allIconAssets().length}</span>
        </button>
      </nav>
      <Show when={workspace() === "mapping" || dirty() || uncertain() || conflicts().length || error()}>
      <div class="im-savebar" data-dirty={dirty()}>
        <span class="im-save-state" role="status" data-dirty={dirty()}>
          {saveState()}
        </span>
        <Show
          when={uncertain() || conflicts().length || !ready()}
          fallback={
            <>
              <button
                type="button"
                disabled={locked() || !dirty()}
                onClick={() => {
                  if (!window.confirm(uxText("放弃所有未保存的图标选择？", "Discard all unsaved icon choices?"))) return
                  setDraft(baseline())
                  setError("")
                  setStatus("")
                }}
                data-action="undo"
              >
                {uxText("放弃更改", "Discard changes")}
              </button>
              <button
                type="button"
                class="im-primary"
                disabled={!canSave()}
                onClick={() => void save()}
                data-action="save"
              >
                {saving()
                  ? uxText("保存中…", "Saving…")
                  : uxText("保存更改", "Save changes")}
              </button>
            </>
          }
        >
          <Show
            when={conflicts().length}
            fallback={
              <button
                type="button"
                class="im-primary"
                disabled={busy()}
                onClick={() => (uncertain() ? void reconcile() : void load())}
                data-action="reconcile"
              >
                {loading()
                  ? uxText("核对中…", "Checking…")
                  : uncertain()
                    ? uxText("核对配置", "Verify settings")
                    : uxText("重新读取", "Retry loading")}
              </button>
            }
          >
            <button
              type="button"
              disabled={busy()}
              onClick={() => {
                setDraft(baseline())
                setConflicts([])
                setError("")
                setStatus("")
              }}
              data-action="undo-conflicts"
            >
              {uxText("撤销草稿", "Discard draft")}
            </button>
            <button
              type="button"
              class="im-primary"
              disabled={busy()}
              onClick={reviewConflicts}
              data-action="review-conflicts"
            >
              {uxText("处理冲突", "Resolve conflicts")}
            </button>
          </Show>
        </Show>
        <Show when={status()}>
          <p class="im-save-feedback" role="status">
            {status()}
          </p>
        </Show>
        <Show when={error()}>
          <p class="im-save-feedback im-error" role="alert">
            {error()}
          </p>
        </Show>
      </div>
      </Show>
      <div hidden={workspace() !== "library"} class="im-library-workspace">
        <IconUploader
          adapter={props.library}
          disabled={locked()}
          busy={setUploadBusy}
          unfinished={setUploadPending}
          catalogLoading={setCatalogLoading}
          previewControl={(open, close) => {
            setUploadPreviewOpen(open)
            closeUploadPreview = close
          }}
        />
        <div class="im-section-heading">
          <div>
            <h2>{uxText("我的素材", "My assets")}</h2>
            <p>
              {uxText(
                "预览、应用到多个类型，或处理 GIF 白底。上传和素材处理即时生效；类型选择需保存。",
                "Preview, assign to multiple types, or remove GIF backgrounds. Asset changes are immediate; icon assignments need saving.",
              )}
            </p>
          </div>
        </div>
        <div class="im-library-tools im-library-filters">
          <input
            type="search"
            aria-label={uxText("搜索素材库", "Search library")}
            placeholder={uxText("搜索素材名称", "Search asset names")}
            value={assetQuery()}
            onInput={(e) => setAssetQuery(e.currentTarget.value)}
          />
          <select aria-label={uxText("素材格式", "Asset format")} value={assetFormat()} onChange={event => setAssetFormat(event.currentTarget.value)}>
            <option value="all">{uxText("全部格式", "All formats")}</option>
            <option value="png">{uxText("静态 PNG", "Static PNG")}</option>
            <option value="gif">{uxText("动态 GIF", "Animated GIF")}</option>
          </select>
          <select aria-label={uxText("使用情况", "Usage")} value={assetUsageFilter()} onChange={event => setAssetUsageFilter(event.currentTarget.value)}>
            <option value="all">{uxText("全部素材", "All assets")}</option>
            <option value="used">{uxText("已使用", "In use")}</option>
            <option value="unused">{uxText("未使用", "Unused")}</option>
          </select>
          <span class="im-count" role="status">
            {libraryAssets().length} {uxText("款素材", "assets")}
          </span>
        </div>
        <Show when={libraryError()}><p class="im-error" role="alert">{libraryError()}</p></Show>
        <Show when={libraryNotice()}><p class="im-success" role="status">{libraryNotice()}</p></Show>
        <div class="im-library-browser">
          <For each={libraryAssets()}>
            {(asset) => (
              <div class="im-library-tile">
              <button
                type="button"
                class="im-library-open"
                aria-haspopup="dialog"
                onClick={(event) => {
                  openLibrary(event.currentTarget, asset.id)
                }}
              >
                <img
                  src={asset.url}
                  alt=""
                  width="80"
                  height="80"
                  loading="lazy"
                />
                <strong>{asset.name}</strong>
              </button>
                <div class="im-format-actions">
                  <small>{iconFormatLabel(asset.name, asset.source)}</small>
                  <TransparentIconAction asset={asset} disabled={locked() || libraryDeleting()} />
                </div>
                <span class="im-usage">{assetUsage(asset.id).length ? uxText(`用于 ${assetUsage(asset.id).length} 类`, `Used by ${assetUsage(asset.id).length} types`) : uxText("未使用", "Unused")}</span>
                <div class="im-library-card-actions">
                  <button type="button" onClick={event => openLibrary(event.currentTarget, asset.id)}>{uxText("选用 / 详情", "Use / details")}</button>
                  <button type="button" disabled={locked() || !!assetUsage(asset.id).length || iconTransparencyBusy(asset.id)} onClick={() => void deleteAsset(asset.id)} aria-label={uxText(`删除 ${asset.name}`, `Delete ${asset.name}`)}>{uxText("删除", "Delete")}</button>
                </div>
              </div>
            )}
          </For>
        </div>
        <Show when={!libraryAssets().length}>
          <div class="im-empty">
            <p>{allIconAssets().length ? uxText("没有符合筛选条件的素材", "No matching assets") : uxText("还没有素材，上传 PNG 或 GIF 开始使用。", "No assets yet. Upload a PNG or GIF to get started.")}</p>
            <button
              type="button"
              onClick={() => {
                setAssetQuery("")
                setAssetFormat("all")
                setAssetUsageFilter("all")
              }}
            >
              {uxText("清除筛选", "Clear filters")}
            </button>
          </div>
        </Show>
      </div>
      <div hidden={workspace() !== "mapping"} class="im-mapping-workspace">
        <div class="im-section-heading">
          <div>
            <h2>{uxText("按文件类型配置", "Configure by file type")}</h2>
            <p>
              {uxText(
                "点击类型选择素材；通用类型会被对应扩展名继承。Ctrl / ⌘ + S 保存。",
                "Choose an icon per type; extensions can inherit category icons. Ctrl / ⌘ + S saves.",
              )}
            </p>
          </div>
        </div>
        <div class="im-filterbar">
          <div class="im-search">
            <input
              type="search"
              aria-label={uxText("搜索全部类型", "Search all types")}
              value={query()}
              onInput={(event) => setQuery(event.currentTarget.value)}
              placeholder={uxText(
                "搜索类型，如 MP3、文件夹",
                "Find a type: MP3, folders…",
              )}
            />
            <Show when={query()}>
              <button
                type="button"
                class="im-search-clear"
                aria-label={uxText("清除搜索", "Clear search")}
                onClick={() => setQuery("")}
              >
                ×
              </button>
            </Show>
          </div>
          <select
            aria-label={uxText("类型分类", "Type category")}
            value={group()}
            disabled={!!query().trim()}
            onChange={(event) => setGroup(event.currentTarget.value)}
          >
            <For each={groups}>
              {([id, chinese, english]) => (
                <option value={id}>{uxText(chinese, english)}</option>
              )}
            </For>
          </select>
        </div>
        <div class="im-tools">
          <button
            type="button"
            class="im-filter-changes"
            aria-pressed={changedOnly()}
            onClick={() => setChangedOnly(!changedOnly())}
          >
            {uxText("只看待保存", "Unsaved only")}
            {dirty() ? ` (${changedIds().length})` : ""}
          </button>
          <details ref={shortcuts} class="im-shortcuts">
            <summary>{uxText("快捷操作", "Quick actions")}</summary>
            <div class="im-shortcut-menu">
              <button
                type="button"
                disabled={locked() || conflicts().length > 0}
                onClick={() => quick("defaults")}
                data-action="defaults"
              >
                {uxText("全部恢复默认", "Use all defaults")}
              </button>
              <button
                type="button"
                disabled={busy()}
                onClick={() => quick("reload")}
                data-action="reload"
              >
                {uxText("重新加载配置", "Reload settings")}
              </button>
              <small>
                {uxText(
                  "恢复默认只更改草稿，保存后生效。",
                  "Defaults change the draft only; save to apply.",
                )}
              </small>
            </div>
          </details>
          <span class="im-count" role="status">
            {query().trim()
              ? uxText(
                  `搜索全部类型 · ${visibleTargets().length} 项`,
                  `All-type search · ${visibleTargets().length} results`,
                )
              : uxText(
                  `${visibleTargets().length} 项`,
                  `${visibleTargets().length} types`,
                )}
          </span>
        </div>
        <div
          class="im-target-grid"
          aria-label={uxText("可替换类型", "Replaceable types")}
        >
          <For each={visibleTargets()}>
            {(item) => (
              <button
                type="button"
                class="im-target"
                data-target={item.id}
                data-changed={changedIds().includes(item.id)}
                aria-haspopup="dialog"
                onClick={(event) => openTarget(item.id, event.currentTarget)}
              >
                <span class="im-target-top">
                  <AssetIcon
                    width="40"
                    height="40"
                    asset={effectiveAsset(draft(), item)}
                    fallback={getDefaultIconForTarget(item)}
                    aria-hidden="true"
                  />
                  <span>
                    {changedIds().includes(item.id)
                      ? uxText("待保存", "Unsaved")
                      : uxText("更换", "Change")}
                  </span>
                </span>
                <strong>{label(item)}</strong>
                <small>{description(item)}</small>
              </button>
            )}
          </For>
        </div>
        <Show when={!visibleTargets().length}>
          <div class="im-empty">
            <p>
              {changedOnly()
                ? uxText(
                    "没有符合条件的待保存更改。",
                    "No unsaved changes match these filters.",
                  )
                : uxText(
                    "没有匹配的类型，搜索会查找全部类型。",
                    "No matching types. Search covers all types.",
                  )}
            </p>
            <button
              type="button"
              onClick={() => {
                setQuery("")
                setChangedOnly(false)
                setGroup("all")
              }}
            >
              {uxText("查看全部类型", "Show all types")}
            </button>
          </div>
        </Show>
      </div>
      <Portal>
        <dialog
          ref={editor}
          class="icon-manager im-picker im-editor"
          tabIndex={-1}
          aria-labelledby="im-picker-title"
          aria-describedby="im-picker-help"
          onKeyDown={trapPickerFocus}
          onCancel={(event) => {
            event.preventDefault()
            closePicker()
          }}
          onClick={(event) => {
            if (event.target !== editor) return
            const r = editor.getBoundingClientRect()
            if (
              event.clientX < r.left ||
              event.clientX > r.right ||
              event.clientY < r.top ||
              event.clientY > r.bottom
            )
              closePicker()
          }}
        >
          <header class="im-picker-head">
            <div>
              <h2 id="im-picker-title">
                {libraryMode()
                  ? uxText("素材详情", "Asset details")
                  : label(target())}
              </h2>
              <small>
                {libraryMode()
                  ? uxText(
                      "选择应用类型，或管理这份素材。",
                      "Choose target types or manage this asset.",
                    )
                  : description(target())}
              </small>
            </div>
            <button
              type="button"
              disabled={libraryDeleting()}
              class="im-picker-close"
              aria-label={uxText("关闭图标选择", "Close icon picker")}
              onClick={closePicker}
            >
              ×
            </button>
          </header>
          <div class="im-picker-body" ref={pickerBody}>
            <Show when={libraryError()}>
              <p class="im-error" role="alert">
                {libraryError()}
              </p>
            </Show>
            <Show when={libraryNotice()}>
              <p class="im-success" role="status">
                {libraryNotice()}
              </p>
            </Show>
            <Show
              when={libraryMode()}
              fallback={
                <>
                  <Show when={conflicts().includes(target().id)}>
                    <div class="im-conflict" role="alert">
                      <strong>
                        {uxText("此类型有冲突", "Conflicting choices")}
                      </strong>
                      <div class="im-conflict-choice">
                        <AssetIcon
                          width="32"
                          height="32"
                          asset={effectiveAsset(baseline(), target())}
                          fallback={getDefaultIconForTarget(target())}
                          aria-hidden="true"
                        />
                        <span>
                          {uxText("服务器：", "Server: ")}
                          {descriptionFor(baseline(), target())}
                        </span>
                      </div>
                      <div class="im-conflict-choice">
                        <AssetIcon
                          width="32"
                          height="32"
                          asset={effective()}
                          fallback={getDefaultIconForTarget(target())}
                          aria-hidden="true"
                        />
                        <span>
                          {uxText("本页：", "This page: ")}
                          {description(target())}
                        </span>
                      </div>
                      <div class="im-conflict-actions">
                        <button
                          type="button"
                          disabled={locked()}
                          data-action="use-server"
                          onClick={() => resolveConflict(target().id, false)}
                        >
                          {uxText("采用服务器", "Use server choice")}
                        </button>
                        <button
                          type="button"
                          disabled={locked()}
                          data-action="keep-local"
                          onClick={() => resolveConflict(target().id, true)}
                        >
                          {uxText("保留本页选择", "Keep my choice")}
                        </button>
                      </div>
                      <small>
                        {uxText(
                          "保留本页会替换服务器此项，点击保存后才生效。",
                          "Keeping your choice replaces this server choice only after saving.",
                        )}
                      </small>
                    </div>
                  </Show>
                  <div class="im-preview" aria-live="polite">
                    <AssetIcon
                      width="64"
                      height="64"
                      asset={effective()}
                      fallback={getDefaultIconForTarget(target())}
                      aria-hidden="true"
                    />
                    <div>
                      <strong>
                        {changedIds().includes(target().id)
                          ? uxText("待保存预览", "Unsaved preview")
                          : uxText("当前图标", "Current icon")}
                      </strong>
                      <div class="im-sample-row">
                        <AssetIcon
                          width="24"
                          height="24"
                          asset={effective()}
                          fallback={getDefaultIconForTarget(target())}
                          aria-hidden="true"
                        />
                        <span>{target().example}</span>
                      </div>
                    </div>
                    <div class="im-card-preview">
                      <AssetIcon
                        width="48"
                        height="48"
                        asset={effective()}
                        fallback={getDefaultIconForTarget(target())}
                        aria-hidden="true"
                      />
                      <small>{uxText("卡片", "Card")}</small>
                    </div>
                  </div>
                  <p id="im-picker-help" class="im-rule">
                    {target().id === "download"
                      ? uxText(
                          "替换文件菜单和工具栏的下载图标，不改播放器控件。",
                          "Changes file-menu and toolbar download icons, not player controls.",
                        )
                      : iconCanInherit(target())
                        ? uxText(
                            "具体格式优先；也可跟随通用图标或单独使用默认。",
                            "Format choices take priority; inherit the category or use its native default.",
                          )
                        : uxText(
                            "点击素材即可预览；关闭后保留选择，统一保存才生效。",
                            "Tap to preview. Choices remain in the draft until you save.",
                          )}
                  </p>
                  <fieldset class="im-choice-fieldset" disabled={locked()}>
                    <legend>{uxText("选择图标", "Choose an icon")}</legend>
                    <div class="im-default-choices">
                      <Show when={iconCanInherit(target())}>
                        <label
                          class="im-choice im-native"
                          data-selected={
                            choice(draft(), target()) === "inherit"
                          }
                        >
                          <input
                            type="radio"
                            name="site-icon-choice"
                            checked={choice(draft(), target()) === "inherit"}
                            onChange={() => select("inherit")}
                          />
                          <span>{uxText("跟随通用", "Inherit category")}</span>
                        </label>
                      </Show>
                      <label
                        class="im-choice im-native"
                        data-selected={
                          choice(draft(), target()) === DEFAULT_ICON
                        }
                      >
                        <input
                          type="radio"
                          name="site-icon-choice"
                          checked={choice(draft(), target()) === DEFAULT_ICON}
                          onChange={() => select(DEFAULT_ICON)}
                        />
                        <AssetIcon
                          width="24"
                          height="24"
                          fallback={getDefaultIconForTarget(target())}
                          aria-hidden="true"
                        />
                        <span>{uxText("默认图标", "Default icon")}</span>
                      </label>
                    </div>
                    <div class="im-library-tools">
                      <input
                        type="search"
                        aria-label={uxText("搜索素材", "Search assets")}
                        value={assetQuery()}
                        onInput={(event) => {
                          setAssetQuery(event.currentTarget.value)
                        }}
                        placeholder={uxText("素材名称", "Asset name")}
                      />
                    </div>
                    <div class="im-asset-grid">
                      <For each={visibleAssets()}>
                        {(asset) => (
                          <label
                            class="im-choice im-asset"
                            data-asset={asset.id}
                            data-selected={
                              choice(draft(), target()) === asset.id
                            }
                          >
                            <input
                              type="radio"
                              name="site-icon-choice"
                              checked={choice(draft(), target()) === asset.id}
                              onChange={() => select(asset.id)}
                            />
                            <img
                              src={asset.url}
                              alt=""
                              width="64"
                              height="64"
                              loading="lazy"
                              decoding="async"
                            />
                            <strong>{asset.name}</strong>
                            <small>
                              {iconFormatLabel(asset.name, asset.source)}
                            </small>
                          </label>
                        )}
                      </For>
                    </div>
                    <Show when={!visibleAssets().length}>
                      <p class="im-empty">
                        {uxText(
                          "暂无素材，可使用默认图标或上传新素材。",
                          "No assets. Use default icons or upload new assets.",
                        )}
                      </p>
                    </Show>
                  </fieldset>
                </>
              }
            >
              <p id="im-picker-help" class="im-rule">
                {uxText(
                  "已应用的素材须先更换并保存，再从素材库删除。",
                  "Replace and save active choices before deleting assets from the library.",
                )}
              </p>
              <Show when={iconAssetById(libraryPreview())} fallback={<p class="im-empty">{uxText("素材已删除，请关闭详情。", "Asset removed. Close this dialog.")}</p>}>
                {asset => <div class="im-asset-detail">
                  <div class="im-library-preview">
                    <img src={asset().url} alt={asset().name} width="160" height="160" />
                    <div>
                      <strong>{asset().name}</strong>
                      <small>{iconFormatLabel(asset().name, asset().source)}</small>
                      <p class="im-usage">{assetUsage(asset().id).length
                        ? uxText(`已保存或草稿使用：${assetUsage(asset().id).map(label).join("、")}`, `Used in saved settings or draft: ${assetUsage(asset().id).map(label).join(", ")}`)
                        : uxText("尚未应用到任何类型", "Not assigned to any type")}</p>
                      <div class="im-detail-actions">
                        <TransparentIconAction asset={asset()} disabled={locked()} />
                        <button type="button" disabled={locked() || uploadBusy() || !!assetUsage(asset().id).length || iconTransparencyBusy(asset().id)} onClick={() => void deleteAsset(asset().id)} data-action="delete-asset">{uxText("删除素材", "Delete asset")}</button>
                      </div>
                    </div>
                  </div>
                  <fieldset class="im-apply-targets" disabled={locked() || conflicts().length > 0}>
                    <legend>{uxText("应用到文件类型", "Assign to file types")}</legend>
                    <p class="im-rule">{uxText("可多选，先加入草稿，再统一保存。", "Select multiple types, add to draft, then save together.")}</p>
                    <input class="im-apply-search" type="search" aria-label={uxText("搜索应用类型", "Search assignment types")} placeholder={uxText("搜索文件夹、MP3、PDF…", "Search folders, MP3, PDF…")} value={applyQuery()} onInput={event => setApplyQuery(event.currentTarget.value)} />
                    <div class="im-apply-options">
                      <For each={assignmentTargets()}>{item => <label>
                        <input type="checkbox" checked={applyTargets().includes(item.id)} onChange={event => setApplyTargets(old => event.currentTarget.checked ? [...old, item.id] : old.filter(id => id !== item.id))} />
                        <span>{label(item)}</span>
                        <Show when={effectiveAsset(draft(), item) === asset().id}><small>{uxText("已选用", "Selected")}</small></Show>
                      </label>}</For>
                      <Show when={!assignmentTargets().length}><p class="im-empty">{uxText("没有匹配的文件类型", "No matching file types")}</p></Show>
                    </div>
                  </fieldset>
                </div>}
              </Show>
            </Show>
          </div>
          <footer class="im-picker-footer">
            <Show when={libraryMode() && iconAssetById(libraryPreview())}>
              <button type="button" class="im-primary" disabled={locked() || conflicts().length > 0 || !applyTargets().length} onClick={applyAsset} data-action="apply-asset">{uxText(`加入待保存（${applyTargets().length}）`, `Add to draft (${applyTargets().length})`)}</button>
            </Show>
            <Show when={!libraryMode()}>
              <div class="im-picker-nav">
                <button
                  type="button"
                  disabled={pickerIndex() <= 0}
                  onClick={() => adjacent(-1)}
                  aria-label={uxText("上一个类型", "Previous type")}
                >
                  ‹
                </button>
                <span>
                  {pickerIndex() + 1} / {pickerTargets().length}
                </span>
                <button
                  type="button"
                  disabled={pickerIndex() >= pickerTargets().length - 1}
                  onClick={() => adjacent(1)}
                  aria-label={uxText("下一个类型", "Next type")}
                >
                  ›
                </button>
              </div>
            </Show>
            <button
              type="button"
              class="im-primary"
              disabled={libraryDeleting()}
              onClick={closePicker}
              data-action="done"
            >
              {libraryMode()
                ? uxText("关闭详情", "Close details")
                : uxText("完成选择", "Done")}
            </button>
            <Show when={!libraryMode()}>
              <small>
                {uxText(
                  dirty() ? `${changedIds().length} 项待保存` : "未更改",
                  dirty()
                    ? `${changedIds().length} unsaved changes`
                    : "No changes",
                )}
              </small>
            </Show>
          </footer>
        </dialog>
      </Portal>
    </section>
  )
}
const IconManager = () => {
  useManageTitle("manage.sidemenu.icons")
  return (
    <Show
      when={UserMethods.is_admin(me())}
      fallback={
        <p role="alert">
          {uxText(
            "请使用管理员账户登录后管理全站图标。",
            "Sign in as an administrator to manage site-wide icons.",
          )}
        </p>
      }
    >
      <IconManagerPanel load={loadIconSettings} save={saveIconSettings} />
    </Show>
  )
}
export default IconManager
