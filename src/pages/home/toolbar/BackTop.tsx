import { Show, createSignal, onCleanup } from "solid-js"
import { Icon } from "@hope-ui/solid"
import { FiArrowUp } from "solid-icons/fi"
import { isMobile } from "~/utils/compatibility"
import { getMainColor } from "~/store"
import { useReducedMotion } from "~/hooks/useReducedMotion"
import { uxText } from "~/utils/ux"
import "./back-top.css"

export const useScrollListener = (
  callback: (e?: Event) => void,
  options?: { immediate?: boolean },
) => {
  if (options?.immediate) callback()
  window.addEventListener("scroll", callback, { passive: true })
  onCleanup(() => window.removeEventListener("scroll", callback))
}

export const BackTop = () => {
  if (isMobile) return null
  const reducedMotion = useReducedMotion()
  const [visible, setVisible] = createSignal(window.scrollY > 100)
  useScrollListener(() => setVisible(window.scrollY > 100))
  return (
    <Show when={visible()}>
      <button
        type="button"
        class="back-top-button"
        aria-label={uxText("返回顶部", "Back to top")}
        title={uxText("返回顶部", "Back to top")}
        style={{ color: getMainColor() }}
        onClick={() =>
          window.scrollTo({
            top: 0,
            behavior: reducedMotion() ? "auto" : "smooth",
          })
        }
      >
        <Icon as={FiArrowUp} boxSize="$6" aria-hidden="true" />
      </button>
    </Show>
  )
}
