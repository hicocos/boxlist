import {
  NavigateOptions,
  SetParams,
  useLocation,
  useNavigate,
  useParams,
  _mergeSearchString,
} from "@solidjs/router"
import { createMemo, untrack } from "solid-js"
import { encodePath, joinBase, log, pathDir, pathJoin, trimBase } from "~/utils"
import { invalidateHistoryHref, recordCurrentDirectory } from "~/store/history"

export type RouterNavigateOptions = Partial<NavigateOptions> & {
  preserveHistory?: boolean
}

const useRouter = () => {
  const navigate = useNavigate()
  const location = useLocation()
  const params = useParams()
  const pathname = createMemo(() => {
    return trimBase(decodeURIComponent(location.pathname))
  })
  const isShare = createMemo(() => {
    return pathname().startsWith("/@s")
  })
  return {
    to: (
      path: string,
      ignore_root?: boolean,
      options?: RouterNavigateOptions,
    ) => {
      if (!ignore_root && path.startsWith("/")) {
        path = joinBase(path)
      }
      log("to:", path)
      recordCurrentDirectory()
      const { preserveHistory, ...navigateOptions } = options ?? {}
      if (!preserveHistory) invalidateHistoryHref(path)
      navigate(path, navigateOptions)
    },
    replace: (to: string) => {
      const path = joinBase(encodePath(pathJoin(pathDir(pathname()), to), true))
      recordCurrentDirectory()
      invalidateHistoryHref(path)
      navigate(path)
    },
    pushHref: (to: string): string => {
      return encodePath(pathJoin(pathname(), to))
    },
    back: () => {
      recordCurrentDirectory()
      navigate(-1)
    },
    forward: () => {
      recordCurrentDirectory()
      navigate(1)
    },
    pathname: pathname,
    isShare: isShare,
    search: location.search,
    searchParams: location.query,
    setSearchParams: (
      params: SetParams,
      options?: Partial<NavigateOptions>,
    ) => {
      const searchString = untrack(() =>
        _mergeSearchString(location.search, params),
      )
      recordCurrentDirectory()
      navigate(location.pathname + searchString + location.hash, {
        scroll: false,
        ...options,
        resolve: true,
      })
    },
    params: params,
  }
}

export { useRouter }
