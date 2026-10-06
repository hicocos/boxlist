import { createComputed, createRoot, createSignal } from "solid-js"
import {
  shouldKeepState,
  ObjStore,
  objStore,
  State,
  password,
} from "~/store/obj"
import { me } from "./user"
import { getPagination } from "./settings"
import { base_path, r } from "~/utils"
import {
  BoundedHistory,
  directoryHref,
  normalizeDirectory,
  parseDirectoryHref,
  validPage,
} from "./navigation-state"
import {
  cancelDirectoryRequests,
  getDirectoryPage,
  setDirectoryPage,
} from "./directory-navigation"

interface History {
  path: string
  obj: object
  page: number
  routePage: number
  scroll: number
  anchor?: { name: string; offset: number }
  openedName?: string
  pagination: string
}

// Memory only: signed object metadata never enters local/sessionStorage.
// Five-minute TTL, 24 directories/pages, approximately 8 MB of JSON at most.
export const HistoryMap = new BoundedHistory<History>()
const [historyRevision, setHistoryRevision] = createSignal(0)
const changed = () => setHistoryRevision((value) => value + 1)
let scope = ""
let credential: unknown
let directoryPassword = ""
let active: { path: string; routePage: number; recorded: boolean } | undefined
let restoreGeneration = 0
const paginationKey = () => JSON.stringify(getPagination())

export const getHistoryScope = () => {
  const user = me()
  return JSON.stringify([
    user.id,
    user.username,
    user.base_path,
    user.role,
    user.permission,
    user.disabled,
  ])
}

export const ensureHistoryScope = () => {
  const next = getHistoryScope()
  const token = r.defaults.headers.common.Authorization
  if (
    scope !== next ||
    credential !== token ||
    directoryPassword !== password()
  ) {
    scope = next
    credential = token
    directoryPassword = password()
    HistoryMap.clear()
    active = undefined
    restoreGeneration++
    cancelDirectoryRequests()
    changed()
  }
  return scope
}

// Store/barrel imports are cyclic; install after module initialization.
queueMicrotask(() =>
  createRoot(() =>
    createComputed(() => {
      ensureHistoryScope()
    }),
  ),
)
window.addEventListener("storage", (event) => {
  if (event.key === "token") {
    HistoryMap.clear()
    active = undefined
    restoreGeneration++
    cancelDirectoryRequests()
    changed()
  }
})

export const setHistoryLocation = (path: string, routePage?: number) => {
  ensureHistoryScope()
  active = {
    path: normalizeDirectory(path),
    routePage: validPage(routePage),
    recorded: false,
  }
}

export const releaseHistoryLocation = () => {
  active = undefined
  restoreGeneration++
}

export const getHistoryKey = (path: string, page?: number) =>
  directoryHref(path, page)

const itemElements = () =>
  Array.from(
    document.querySelectorAll<HTMLElement>(
      ".obj-box .list-item[data-name], .obj-box .grid-item[data-name], .obj-box .image-item[data-name]",
    ),
  )
const itemName = (element: HTMLElement) => element.dataset.name

export const recordHistory = (
  path: string,
  page?: number,
  source?: Element | null,
) => {
  ensureHistoryScope()
  const directory = normalizeDirectory(path)
  if (
    !active ||
    active.path !== directory ||
    active.recorded ||
    objStore.err ||
    ![State.FetchingMore, State.Folder].includes(objStore.state)
  )
    return
  const items = itemElements()
  const clickedRow = source?.closest<HTMLElement>("[data-name]")
  const clicked =
    clickedRow && items.includes(clickedRow) ? clickedRow : undefined
  const element =
    clicked ??
    items.find((item) => {
      const rect = item.getBoundingClientRect()
      return rect.bottom > 0 && rect.top < window.innerHeight
    })
  const name = element && itemName(element)
  const obj = JSON.parse(JSON.stringify(objStore))
  obj.state = State.Folder
  const routePage = validPage(page ?? active.routePage)
  const history: History = {
    path: directory,
    obj,
    page: getDirectoryPage(),
    routePage,
    scroll: window.scrollY,
    anchor:
      name && element
        ? { name, offset: element.getBoundingClientRect().top }
        : undefined,
    // A viewport anchor is not necessarily the item the user opened.
    openedName: clicked ? itemName(clicked) : undefined,
    pagination: paginationKey(),
  }
  HistoryMap.set(
    getHistoryKey(directory, routePage),
    history,
    JSON.stringify(history).length * 2,
  )
  changed()
}

/** Call BEFORE navigation, including before leaving for a standalone reader. */
export const recordCurrentDirectory = (source?: Element | null) => {
  if (!active) return
  recordHistory(active.path, active.routePage, source)
  if (active) active.recorded = true
}

const lookupHistory = (path: string, page?: number) => {
  ensureHistoryScope()
  const history = HistoryMap.get(getHistoryKey(path, page))
  return history?.pagination === paginationKey() ? history : undefined
}

/** App-relative encoded href, intentionally without joinBase or passwords. */
export const getDirectoryReturnHref = (directory: string): string => {
  historyRevision()
  ensureHistoryScope()
  const path = normalizeDirectory(directory)
  const histories = HistoryMap.values().filter(
    (history) =>
      history.path === path && history.pagination === paginationKey(),
  )
  const history = histories[histories.length - 1]
  return directoryHref(
    path,
    getPagination().type === "pagination" ? history?.routePage : undefined,
  )
}

export const recoverHistory = async (
  path: string,
  page?: number,
  isCurrent = () => true,
): Promise<boolean> => {
  const history = lookupHistory(path, page)
  if (!history || !isCurrent()) return false
  const generation = ++restoreGeneration
  setDirectoryPage(history.page)
  shouldKeepState() || ObjStore.setState(State.Initial)
  ObjStore.set(JSON.parse(JSON.stringify(history.obj)))
  setHistoryLocation(path, page)
  // Folder and its layout are lazy. Wait for actual items rather than two timers.
  await new Promise<void>((resolve) => {
    let frames = 0
    const restore = () => {
      if (generation !== restoreGeneration || !isCurrent()) {
        resolve()
        return
      }
      const element =
        history.anchor &&
        itemElements().find((item) => itemName(item) === history.anchor!.name)
      if (
        !element &&
        frames++ < 90 &&
        (history.obj as { objs?: unknown[] }).objs?.length
      ) {
        requestAnimationFrame(restore)
        return
      }
      window.scrollTo({ top: history.scroll, behavior: "instant" })
      if (element && history.anchor) {
        window.scrollBy({
          top: element.getBoundingClientRect().top - history.anchor.offset,
          behavior: "instant",
        })
      }
      if (history.openedName) {
        const opened = itemElements().find(
          (item) => itemName(item) === history.openedName,
        )
        if (opened) {
          opened.classList.add("directory-return-highlight")
          setTimeout(
            () => opened.classList.remove("directory-return-highlight"),
            1400,
          )
        }
      }
      resolve()
    }
    requestAnimationFrame(restore)
  })
  return isCurrent()
}

export const hasHistory = (path: string, page?: number) =>
  !!lookupHistory(path, page)

/** Invalidate this directory only (all its pages unless a page is supplied). */
export const clearHistory = (path: string, page?: number) => {
  ensureHistoryScope()
  const directory = normalizeDirectory(path)
  if (page !== undefined) HistoryMap.delete(getHistoryKey(directory, page))
  else
    for (const history of HistoryMap.values()) {
      if (history.path === directory)
        HistoryMap.delete(getHistoryKey(directory, history.routePage))
    }
  changed()
}

export const invalidateHistoryHref = (href: string) => {
  const target = parseDirectoryHref(href, location.href, base_path)
  if (target)
    clearHistory(
      target.path,
      getPagination().type === "pagination" ? target.page : undefined,
    )
}

// Capture runs before router bubbling/unmount/scroll reset. Keep explicit return
// links; normal navigations still invalidate only their destination.
document.addEventListener(
  "click",
  (event) => {
    if (
      event.button !== 0 ||
      event.ctrlKey ||
      event.metaKey ||
      event.shiftKey ||
      event.altKey
    )
      return
    const source = event.target instanceof Element ? event.target : null
    const link = source?.closest<HTMLAnchorElement>("a[href]")
    if (
      !link ||
      link.hasAttribute("download") ||
      (link.target && link.target !== "_self")
    )
      return
    const href = link.getAttribute("href")!
    const target = parseDirectoryHref(href, location.href, base_path)
    if (!target) return
    const sourceLocation = location.href
    const sourceBinding = active
    recordCurrentDirectory(source)
    // Solid Router starts navigation in a microtask. Resetting in our earlier
    // capture microtask let Obj overwrite the clicked row with the first visible
    // row before the transition committed. Wait until the event task has settled;
    // only a genuinely non-navigating gallery/selection click may unlock it.
    setTimeout(() => {
      if (
        active === sourceBinding &&
        location.href === sourceLocation &&
        active
      )
        active.recorded = false
    }, 0)
    if (!link.hasAttribute("data-restore-history")) invalidateHistoryHref(href)
  },
  true,
)

// popstate runs after URL change, so record the bound source, not location.href.
window.addEventListener("popstate", () => recordCurrentDirectory())
