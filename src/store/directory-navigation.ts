import { createSignal } from "solid-js"
import { NavigationEpoch, type DirectoryErrorKind } from "./navigation-state"

export interface DirectoryFailure {
  kind: DirectoryErrorKind
  code?: number
  /** Only sanitized classification/status, not raw provider messages. */
  details: string
  page?: number
  refresh?: boolean
}

export const directoryEpoch = new NavigationEpoch()
export const [directoryFailure, setDirectoryFailure] =
  createSignal<DirectoryFailure>()
export const [pageFailure, setPageFailure] = createSignal<DirectoryFailure>()
export const [directoryBusy, setDirectoryBusy] = createSignal(false)
export const [getDirectoryPage, setDirectoryPage] = createSignal(1)

let cancelRequests: (() => void)[] = []
export const registerDirectoryRequest = (cancel: () => void) => {
  cancelRequests.push(cancel)
  return () => {
    cancelRequests = cancelRequests.filter((item) => item !== cancel)
  }
}
export const cancelDirectoryRequests = () => {
  directoryEpoch.begin()
  const requests = cancelRequests
  cancelRequests = []
  requests.forEach((cancel) => cancel())
  setDirectoryBusy(false)
}
