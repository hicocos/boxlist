import { Button, Checkbox } from "@hope-ui/solid"
import { createSignal, onCleanup, Show } from "solid-js"
import { useRouter, useT } from "~/hooks"
import { me, objStore, State } from "~/store"
import { UserMethods } from "~/types"
import type { DerivativeJob } from "~/types/derivatives"
import { validDerivativePath } from "~/utils/derivative-policy"
import { derivativeAPI } from "~/utils/derivatives"
import { ModalWrapper } from "./ModalWrapper"
import "~/pages/manage/tasks/derivatives.css"

const GenerateForm = () => {
  const t = useT()
  const { pathname, to } = useRouter()
  const [recursive, setRecursive] = createSignal(true)
  const [thumbOnly, setThumbOnly] = createSignal(false)
  const [force, setForce] = createSignal(false)
  const [busy, setBusy] = createSignal(false)
  const [error, setError] = createSignal("")
  const controller = new AbortController()
  onCleanup(() => controller.abort())
  const submit = async (event: SubmitEvent) => {
    event.preventDefault()
    if (busy() || !UserMethods.is_admin(me()) || objStore.state !== State.Folder || !validDerivativePath(pathname())) return
    setBusy(true)
    setError("")
    try {
      const job = await derivativeAPI<DerivativeJob>("/jobs", {
        path: pathname(), recursive: recursive(), profiles: thumbOnly() ? ["thumb"] : ["thumb", "reader"], force: force(),
      }, controller.signal)
      if (!controller.signal.aborted) to(`/@manage/tasks/derivatives?job=${encodeURIComponent(job.id)}`)
    } catch {
      if (!controller.signal.aborted) setError(t("derivatives.action_failed"))
    } finally { if (!controller.signal.aborted) setBusy(false) }
  }
  return <form class="derivative-form" onSubmit={submit}>
    <p class="derivative-path"><strong>{t("derivatives.path")}: </strong>{pathname()}</p>
    <Checkbox checked={recursive()} onChange={event => setRecursive(event.currentTarget.checked)}>{t("derivatives.recursive")}</Checkbox>
    <Checkbox checked={thumbOnly()} onChange={event => setThumbOnly(event.currentTarget.checked)}>{t("derivatives.thumb_only")}</Checkbox>
    <Checkbox checked={force()} onChange={event => setForce(event.currentTarget.checked)}>{t("derivatives.force")}</Checkbox>
    <Show when={force()}><p>{t("derivatives.force_hint")}</p></Show>
    <p>{t("derivatives.scope_hint")}</p>
    <p>{t("derivatives.generate_hint")}</p>
    <Show when={error()}><p role="alert">{error()}</p></Show>
    <Button type="submit" loading={busy()}>{t("derivatives.start")}</Button>
  </form>
}
export const GenerateDerivatives = () => <Show when={UserMethods.is_admin(me())}>
  <ModalWrapper name="generate_derivatives" title="derivatives.generate"><GenerateForm /></ModalWrapper>
</Show>
