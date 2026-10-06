import { createSignal, onCleanup } from "solid-js"

/** Read and subscribe without changing layout or native scrolling. */
export const useReducedMotion = () => {
  const query = window.matchMedia("(prefers-reduced-motion: reduce)")
  const [reduced, setReduced] = createSignal(query.matches)
  const update = () => setReduced(query.matches)
  query.addEventListener("change", update)
  onCleanup(() => query.removeEventListener("change", update))
  return reduced
}
