// Serialize every customize_head write from this frontend across same-origin
// tabs. Hold the lock from the latest read through write + exact verification.
// Fail closed in unsupported/insecure browsers instead of risking lost HTML.
export const withHeadWriteLock = async <T>(
  operation: () => Promise<T>,
  locks: Pick<LockManager, "request"> | undefined = navigator.locks,
): Promise<T> => {
  if (!locks?.request) {
    throw new Error(
      "当前浏览器不支持安全保存，请使用 HTTPS 下的最新版 Chrome、Edge、Firefox 或 Safari。",
    )
  }
  return locks.request(
    "openlist-customize-head-write",
    { mode: "exclusive" },
    operation,
  )
}
