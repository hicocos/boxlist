type LeaveGuard = { allow: () => boolean; href: string; state: unknown }
let current: LeaveGuard | undefined
// Import this module before creating Router. Native popstate is dispatched to
// Window itself; an earlier Router listener can unmount a later guard before
// that guard runs, even when the later listener requested capture.
window.addEventListener(
  "popstate",
  (event) => {
    const guard = current
    if (!guard || location.href === guard.href || guard.allow()) return
    event.stopImmediatePropagation()
    history.pushState(guard.state, "", guard.href)
  },
  true,
)
export const registerIconLeaveGuard = (guard: LeaveGuard) => {
  current = guard
  return () => {
    if (current === guard) current = undefined
  }
}
