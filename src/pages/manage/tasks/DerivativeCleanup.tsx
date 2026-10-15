import { createEffect, createSignal, For, on, onCleanup, Show } from "solid-js"
import { useT } from "~/hooks"
import { getFileSize } from "~/utils"
import { derivativeAPI } from "~/utils/derivatives"

type CleanupMode = "temporary" | "stale"
type Assessment = Record<CleanupMode, { files: number; bytes: number }>
type Verification = { checked: number; missing: number }

export default function DerivativeCleanup(props: { busy: boolean; clean: (mode: CleanupMode) => void; navigate: (tab: "jobs" | "rules") => void }) {
  const t = useT()
  const [result, setResult] = createSignal<Assessment>()
  const [checking, setChecking] = createSignal(false)
  const [error, setError] = createSignal(false)
  const [checkedAt, setCheckedAt] = createSignal("")
  const [reload, setReload] = createSignal(0)
  const [verifying, setVerifying] = createSignal(false)
  const [verifyState, setVerifyState] = createSignal<"idle" | "running" | "success" | "failed" | "cancelled">("idle")
  const [verification, setVerification] = createSignal<Verification>()
  let verifyController: AbortController | undefined
  let disposed = false
  onCleanup(() => {
    disposed = true
    verifyController?.abort()
  })
  const verify = async () => {
    if (props.busy || verifying()) return
    const controller = new AbortController()
    verifyController = controller
    setVerification(undefined)
    setVerifyState("running")
    setVerifying(true)
    try {
      const response = await derivativeAPI<Verification>("/verify", {}, controller.signal, 600000)
      if (!disposed && !controller.signal.aborted) {
        setVerification(response)
        setVerifyState("success")
      }
    } catch {
      if (!disposed && !controller.signal.aborted) setVerifyState("failed")
    } finally {
      verifyController = undefined
      if (!disposed) setVerifying(false)
    }
  }
  const cancelVerification = () => {
    if (!verifying()) return
    setVerifyState("cancelled")
    verifyController?.abort()
  }
  createEffect(on([() => props.busy, verifying, reload], ([busy, verifying]) => {
    const controller = new AbortController()
    onCleanup(() => controller.abort())
    setResult(undefined)
    setChecking(!busy && !verifying)
    setError(false)
    if (busy || verifying) return
    void (async () => {
      try {
        const assessment = await derivativeAPI<Assessment>("/cleanup", undefined, controller.signal, 60000)
        if (!controller.signal.aborted) {
          setResult(assessment)
          setCheckedAt(new Date().toLocaleTimeString())
        }
      } catch {
        if (!controller.signal.aborted) setError(true)
      } finally {
        if (!controller.signal.aborted) setChecking(false)
      }
    })()
  }))
  return <section class="dc-cleanup">
    <div class="dc-section-heading">
      <h3>{t("derivatives.console.maintenance")}</h3>
      <button class="dc-button" disabled={checking() || props.busy || verifying()} onClick={() => setReload(value => value + 1)}>{t(`derivatives.console.${checking() ? "cleanup_checking" : "cleanup_check"}`)}</button>
    </div>
    <p class="dc-muted">{t("derivatives.console.verify_hint")}</p>
    <div class="derivative-actions">
      <button class="dc-button" disabled={props.busy || verifying()} onClick={() => void verify()}>{t("derivatives.console.verify_button")}</button>
      <Show when={verifying()}><button class="dc-button" disabled={verifyState() === "cancelled"} onClick={cancelVerification}>{t("derivatives.console.verify_cancel")}</button></Show>
    </div>
    <Show when={verifyState() === "running"}><p class="dc-muted" role="status">{t("derivatives.console.verify_running")}</p></Show>
    <Show when={verifyState() === "success" && verification()}>{value => <p class="dc-muted" role="status">{t("derivatives.console.verify_done", { checked: value().checked, missing: value().missing })}</p>}</Show>
    <Show when={verifyState() === "failed"}><p class="dc-alert" role="alert">{t("derivatives.console.verify_failed")}</p></Show>
    <Show when={verifyState() === "cancelled"}><p class="dc-muted" role="status">{t("derivatives.console.verify_cancelled")}</p></Show>
    <Show when={error()}><p class="dc-alert" role="alert">{t("derivatives.console.cleanup_check_failed")}</p></Show>
    <Show when={result()}><p class="dc-muted" role="status">{t("derivatives.console.cleanup_checked", { time: checkedAt() })}</p></Show>
    <div class="dc-cleanup-options"><For each={["temporary", "stale"] as const}>{mode => <div>
      <div>
        <h4>{t(`derivatives.cleanup_${mode}`)}</h4>
        <p class="dc-muted">{t(`derivatives.console.cleanup_${mode}_hint`)}</p>
        <p class="dc-cleanup-amount" role="status"><Show when={result()} fallback={t(`derivatives.console.${checking() ? "cleanup_checking" : "cleanup_unknown"}`)}>{assessment => assessment()[mode].files > 0
          ? t("derivatives.console.cleanup_amount", { count: assessment()[mode].files, size: getFileSize(assessment()[mode].bytes) })
          : t("derivatives.console.cleanup_empty")}</Show></p>
      </div>
      <button class="dc-button" disabled={props.busy || verifying() || checking() || !result() || result()![mode].files === 0} onClick={() => { if (!verifying() && !props.busy) props.clean(mode) }}>{t(`derivatives.console.cleanup_${mode}_button`)}</button>
    </div>}</For></div>
    <details class="dc-details"><summary>{t("derivatives.console.cleanup_detection")}</summary><p>{t("derivatives.console.cleanup_detection_hint")}</p><div class="derivative-actions"><button class="dc-button" disabled={props.busy} onClick={() => props.navigate("jobs")}>{t("derivatives.console.cleanup_scan_tasks")}</button><button class="dc-button" disabled={props.busy} onClick={() => props.navigate("rules")}>{t("derivatives.console.cleanup_scan_rules")}</button></div></details>
  </section>
}
