import axios, { type CancelToken } from "axios"
import { batch } from "solid-js"
import {
  password,
  ObjStore,
  State,
  getPagination,
  objStore,
  shouldKeepState,
  me,
} from "~/store"
import {
  clearHistory,
  ensureHistoryScope,
  hasHistory,
  recoverHistory,
  setHistoryLocation,
} from "~/store/history"
import {
  cancelDirectoryRequests,
  directoryBusy,
  directoryEpoch,
  getDirectoryPage,
  pageFailure,
  registerDirectoryRequest,
  setDirectoryBusy,
  setDirectoryFailure,
  setDirectoryPage,
  setPageFailure,
} from "~/store/directory-navigation"
import {
  BoundedHistory,
  classifyDirectoryError,
  collectDirectoryPages,
  directoryErrorDetails,
  directoryHref,
  mergeDirectoryPage,
  validPage,
} from "~/store/navigation-state"
import { fsGet, fsList, pathJoin, notify } from "~/utils"
import { uxText } from "~/utils/ux"
import type { FsListResp, Resp } from "~/types"
import { useRouter } from "./useRouter"

const knownDirectories = new BoundedHistory<boolean>(128, 128)
let knownScope = ""
const failedBackgroundLists = new BoundedHistory<boolean>(32, 32)
let initialBasePathRedirect = true

export const getGlobalPage = getDirectoryPage
export const setGlobalPage = (page: number) => setDirectoryPage(validPage(page))
export const resetGlobalPage = () => setGlobalPage(1)

/** Existing fs API supports CancelToken, but its shared client has no timeout. */
const request = async <T>(
  send: (token: CancelToken) => Promise<Resp<T>>,
): Promise<Resp<T>> => {
  const source = axios.CancelToken.source()
  const unregister = registerDirectoryRequest(() =>
    source.cancel("Directory navigation changed"),
  )
  let timedOut = false
  const timer = setTimeout(() => {
    timedOut = true
    source.cancel("Directory request timed out")
  }, 30_000)
  try {
    const response = await send(source.token)
    return timedOut
      ? { code: 408, message: "Request timed out", data: undefined as T }
      : response
  } catch (error) {
    return {
      code: timedOut ? 408 : axios.isCancel(error) ? -1 : 0,
      message: timedOut ? "Request timed out" : "Network request failed",
      data: undefined as T,
    }
  } finally {
    clearTimeout(timer)
    unregister()
  }
}

export const usePath = () => {
  let retryPassword = false
  const { pathname, to } = useRouter()
  const pagination = getPagination()
  const syncScope = () => {
    const scope = ensureHistoryScope()
    if (knownScope !== scope) {
      knownDirectories.clear()
      failedBackgroundLists.clear()
      knownScope = scope
    }
    return scope
  }
  const setPathAs = (path: string, dir = true, push = false) => {
    syncScope()
    if (push) path = pathJoin(pathname(), path)
    if (dir) knownDirectories.set(path, true)
    else knownDirectories.delete(path)
  }

  const context = () => {
    const scope = syncScope()
    const epoch = directoryEpoch.current()
    const route = pathname()
    return () =>
      syncScope() === scope &&
      directoryEpoch.matches(epoch) &&
      pathname() === route
  }
  const begin = () => {
    syncScope()
    cancelDirectoryRequests()
    setDirectoryFailure(undefined)
    setPageFailure(undefined)
    ObjStore.setErr("")
    return context()
  }

  const fail = (
    message: string,
    code?: number,
    options?: { page?: number; refresh?: boolean; preserveList?: boolean },
  ) => {
    if (code === -1) return
    const kind = classifyDirectoryError(message, code)
    const failure = {
      kind,
      code,
      details: directoryErrorDetails(kind, code),
      page: options?.page,
      refresh: options?.refresh,
    }
    // Never retain privileged cached rows after an explicit access rejection.
    if (kind === "permission" || kind === "password") {
      clearHistory(pathname())
      ObjStore.setObjs([])
      if (kind === "password") {
        ObjStore.setErr("")
        setDirectoryFailure(failure)
        ObjStore.setState(State.NeedPassword)
        if (retryPassword)
          notify.error(
            uxText(
              "目录密码不正确，请重新输入。",
              "The directory password is incorrect. Please try again.",
            ),
          )
        return
      }
    } else if (options?.preserveList) {
      setPageFailure(failure)
      ObjStore.setState(State.Folder)
      return
    }
    setDirectoryFailure(failure)
    ObjStore.setErr(failure.details)
  }

  const applyList = (data: FsListResp["data"], items = data.content ?? []) => {
    batch(() => {
      ObjStore.setObjs(items)
      ObjStore.setTotal(Math.max(0, data.total))
      ObjStore.setReadme(data.readme)
      ObjStore.setHeader(data.header)
      ObjStore.setWrite(data.write)
      ObjStore.setWriteContentBypass(data.write_content_bypass)
      ObjStore.setProvider(data.provider)
      ObjStore.setDirectUploadTools(data.direct_upload_tools)
      shouldKeepState() || ObjStore.setState(State.Folder)
    })
  }

  const handleFolder = async (
    path: string,
    index?: number,
    size?: number,
    append = false,
    force?: boolean,
    onlyList = false,
  ): Promise<boolean> => {
    const isCurrent = context()
    const page = validPage(index)
    const pageSize =
      pagination.type === "all" ? undefined : (size ?? pagination.size)
    const backgroundKey = `${path}:${page}`
    if (
      directoryBusy() ||
      (onlyList && failedBackgroundLists.get(backgroundKey))
    )
      return false
    const hasRows = !!objStore.objs.length
    if (append && (page <= getGlobalPage() || (hasRows && allLoaded())))
      return false
    setDirectoryBusy(true)
    setPageFailure(undefined)
    if (!onlyList && !shouldKeepState())
      ObjStore.setState(append ? State.FetchingMore : State.FetchingObjs)
    try {
      const response = await request((token) =>
        fsList(path, password(), page, pageSize, force, token),
      )
      if (!isCurrent()) return false
      if (response.code !== 200) {
        if (onlyList) failedBackgroundLists.set(backgroundKey, true)
        else
          fail(response.message, response.code, { page, preserveList: append })
        return false
      }
      const data = response.data
      const total = Math.max(0, data.total)
      const maxPage = Math.max(1, Math.ceil(total / (pageSize || total || 1)))
      if (!onlyList && pagination.type === "pagination" && page > maxPage) {
        to(directoryHref(path, maxPage), false, {
          replace: true,
          preserveHistory: true,
        })
        return false
      }
      const items = append
        ? mergeDirectoryPage([...objStore.objs], data.content ?? [])
        : (data.content ?? [])
      batch(() => {
        setGlobalPage(Math.min(page, maxPage))
        if (onlyList) {
          ObjStore.setObjs(items)
          ObjStore.setTotal(total)
        } else applyList(data, append ? items.slice(0, total) : items)
      })
      knownDirectories.set(path, true)
      return true
    } finally {
      if (isCurrent()) {
        setDirectoryBusy(false)
        if (objStore.state === State.FetchingMore)
          ObjStore.setState(State.Folder)
      }
    }
  }

  const handleObj = async (
    path: string,
    index?: number,
    force?: boolean,
  ): Promise<boolean> => {
    const isCurrent = context()
    shouldKeepState() || ObjStore.setState(State.FetchingObj)
    setDirectoryBusy(true)
    const response = await request((token) => fsGet(path, password(), token))
    if (!isCurrent()) return false
    setDirectoryBusy(false)
    if (response.code !== 200) {
      const base = me().base_path.replace(/\/$/, "")
      if (
        initialBasePathRedirect &&
        base &&
        base !== "/" &&
        (path === base || path.startsWith(`${base}/`)) &&
        classifyDirectoryError(response.message, response.code) === "missing"
      ) {
        initialBasePathRedirect = false
        to(directoryHref(path.slice(base.length) || "/", index), false, {
          replace: true,
        })
        return false
      }
      fail(response.message, response.code)
      return false
    }
    const data = response.data
    ObjStore.setObj(data)
    ObjStore.setProvider(data.provider)
    if (data.is_dir) {
      setPathAs(path)
      return handleFolder(path, index, undefined, false, force)
    }
    batch(() => {
      ObjStore.setReadme(data.readme)
      ObjStore.setHeader(data.header)
      ObjStore.setRelated(data.related ?? [])
      ObjStore.setRawUrl(data.raw_url)
      shouldKeepState() || ObjStore.setState(State.File)
    })
    return true
  }

  const handlePathChange = async (
    path: string,
    index?: number,
    retryPass?: boolean,
    force?: boolean,
  ): Promise<boolean> => {
    const isCurrent = begin()
    setHistoryLocation(path, index)
    retryPassword = !!retryPass
    if (!force && hasHistory(path, index))
      return recoverHistory(path, index, isCurrent)
    setGlobalPage(index ?? 1)
    if (knownDirectories.get(path))
      return handleFolder(path, index, undefined, false, force)
    return handleObj(path, index, force)
  }

  const allLoaded = () =>
    pagination.type === "all" ||
    getGlobalPage() >= Math.ceil(objStore.total / Math.max(1, pagination.size))
  const loadMore = () => {
    if (directoryBusy() || pageFailure() || allLoaded())
      return Promise.resolve(false)
    return handleFolder(pathname(), getGlobalPage() + 1, undefined, true)
  }

  const refresh = async (
    retryPassword?: boolean,
    force?: boolean,
  ): Promise<boolean> => {
    if (directoryBusy()) return false
    const path = pathname()
    const scroll = window.scrollY
    const target = getGlobalPage()
    const retainList =
      [State.Folder, State.FetchingMore].includes(objStore.state) &&
      !objStore.err
    clearHistory(path)
    if (!retainList) return handlePathChange(path, target, retryPassword, force)
    const isCurrent = begin()
    setDirectoryBusy(true)
    // Stage the whole refresh: a failed later page must not destroy the visible
    // list or mix old and new pages. A retry restarts this one transaction.
    let lastData: FsListResp["data"] | undefined
    let failedPage = 1
    const cumulative =
      pagination.type === "load_more" || pagination.type === "auto_load_more"
    try {
      const result = await collectDirectoryPages({
        target: cumulative ? target : 1,
        size: pagination.type === "all" ? 0 : pagination.size,
        isCurrent,
        fetch: async (page) => {
          const requestedPage = cumulative
            ? page
            : pagination.type === "pagination"
              ? target
              : 1
          failedPage = requestedPage
          const response = await request((token) =>
            fsList(
              path,
              password(),
              requestedPage,
              pagination.type === "all" ? undefined : pagination.size,
              force && page === 1,
              token,
            ),
          )
          if (!isCurrent()) return undefined
          if (response.code !== 200) {
            fail(response.message, response.code, {
              page: failedPage,
              refresh: true,
              preserveList: true,
            })
            return undefined
          }
          lastData = response.data
          return {
            items: response.data.content ?? [],
            total: response.data.total,
          }
        },
      })
      if (!result.ok || !lastData || !isCurrent()) return false
      if (
        pagination.type === "pagination" &&
        target > Math.max(1, Math.ceil(result.total / pagination.size))
      ) {
        to(
          directoryHref(
            path,
            Math.max(1, Math.ceil(result.total / pagination.size)),
          ),
          false,
          { replace: true, preserveHistory: true },
        )
        return false
      }
      applyList(
        lastData,
        mergeDirectoryPage([], result.items).slice(0, result.total),
      )
      setGlobalPage(cumulative ? result.page : target)
      setHistoryLocation(
        path,
        pagination.type === "pagination" ? target : undefined,
      )
      requestAnimationFrame(() => {
        if (isCurrent()) window.scrollTo({ top: scroll, behavior: "instant" })
      })
      return true
    } finally {
      if (isCurrent()) setDirectoryBusy(false)
    }
  }

  const retryPage = () => {
    const failure = pageFailure()
    if (!failure || directoryBusy()) return Promise.resolve(false)
    setPageFailure(undefined)
    return failure.refresh
      ? refresh()
      : handleFolder(
          pathname(),
          failure.page ?? getGlobalPage() + 1,
          undefined,
          true,
        )
  }
  return {
    handlePathChange,
    handleFolder,
    setPathAs,
    refresh,
    loadMore,
    retryPage,
    allLoaded,
  }
}
