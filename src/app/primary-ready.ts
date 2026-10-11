import { createSignal } from "solid-js"

export const [primaryReady, setPrimaryReady] = createSignal(false)

// One document-level milestone, after authorized primary content has painted.
export function markPrimaryReady() {
  if (window.__openlistPrimaryReady) return
  requestAnimationFrame(() => requestAnimationFrame(() => {
    if (window.__openlistPrimaryReady) return
    window.__openlistPrimaryReady = true
    setPrimaryReady(true)
    window.dispatchEvent(new Event("openlist:primary-ready"))
  }))
}
