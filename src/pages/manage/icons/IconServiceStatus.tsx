import { createSignal, onCleanup, onMount } from "solid-js"
import { base_path } from "~/utils/config"
import { uxText } from "~/utils/ux"

/** Integrated edition: same-origin only; no token and no arbitrary endpoint. */
export const IconServiceStatus = () => {
  const [busy, setBusy] = createSignal(false)
  const [message, setMessage] = createSignal("")
  let controller: AbortController | undefined
  let active = true
  const check = async () => {
    if (busy()) return
    controller = new AbortController()
    const current = controller
    const timeout = setTimeout(() => current.abort(), 8000)
    setBusy(true)
    setMessage(uxText("正在检查内置素材接口…", "Checking built-in icon API…"))
    try {
      const response = await fetch(`${base_path}/icon/api/health`, {
        signal: current.signal,
        cache: "no-store",
        credentials: "omit",
        redirect: "error",
        headers: { Accept: "application/json" },
      })
      if (!response.ok || !response.headers.get("content-type")?.includes("application/json"))
        throw new Error(uxText("接口返回异常，请确认使用集成版后端。", "Unexpected response; use the integrated backend."))
      const data = await response.json()
      if (data.code !== 200 || data.data?.version !== 1)
        throw new Error(uxText("素材接口版本不兼容。", "Incompatible icon API version."))
      if (active)
        setMessage(uxText("内置素材接口可用，无需单独部署或填写地址。", "Built-in icon API is available; no separate service or address is needed."))
    } catch (cause) {
      if (active)
        setMessage(current.signal.aborted
          ? uxText("连接超时，请重试。", "Connection timed out; retry.")
          : cause instanceof Error ? cause.message : uxText("连接失败，请重试。", "Connection failed; retry."))
    } finally {
      clearTimeout(timeout)
      if (active) setBusy(false)
      if (controller === current) controller = undefined
    }
  }
  onMount(() => void check())
  onCleanup(() => { active = false; controller?.abort() })
  return (
    <div class="im-service-status">
      <p role="status">{message()}</p>
      <button type="button" disabled={busy()} onClick={() => void check()}>
        {busy() ? uxText("检查中…", "Checking…") : uxText("测试连接", "Test connection")}
      </button>
    </div>
  )
}
