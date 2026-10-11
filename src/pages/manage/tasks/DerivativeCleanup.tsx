import { createEffect, createSignal, For, on, onCleanup, Show } from "solid-js"
import { useT } from "~/hooks"
import { getFileSize } from "~/utils"
import { derivativeAPI } from "~/utils/derivatives"

type CleanupMode = "temporary" | "stale"
type Assessment = Record<CleanupMode, { files: number; bytes: number }>

export default function DerivativeCleanup(props: { busy: boolean; clean: (mode: CleanupMode) => void; navigate: (tab: "jobs" | "rules") => void }) {
  const t = useT()
  const [result, setResult] = createSignal<Assessment>()
  const [checking, setChecking] = createSignal(false)
  const [error, setError] = createSignal(false)
  const [checkedAt, setCheckedAt] = createSignal("")
  const [reload, setReload] = createSignal(0)
  createEffect(on([() => props.busy, reload], ([busy]) => {
    const controller = new AbortController()
    onCleanup(() => controller.abort())
    setResult(undefined)
    setChecking(!busy)
    setError(false)
    if (busy) return
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
      <button class="dc-button" disabled={checking() || props.busy} onClick={() => setReload(value => value + 1)}>{t(`derivatives.console.${checking() ? "cleanup_checking" : "cleanup_check"}`)}</button>
    </div>
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
      <button class="dc-button" disabled={props.busy || checking() || !result() || result()![mode].files === 0} onClick={() => props.clean(mode)}>{t(`derivatives.console.cleanup_${mode}_button`)}</button>
    </div>}</For></div>
    <details class="dc-details"><summary>{t("derivatives.console.cleanup_detection")}</summary><p>{t("derivatives.console.cleanup_detection_hint")}</p><div class="derivative-actions"><button class="dc-button" disabled={props.busy} onClick={() => props.navigate("jobs")}>{t("derivatives.console.cleanup_scan_tasks")}</button><button class="dc-button" disabled={props.busy} onClick={() => props.navigate("rules")}>{t("derivatives.console.cleanup_scan_rules")}</button></div></details>
  </section>
}
