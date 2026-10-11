import { createSignal, Show } from "solid-js"
import { useT } from "~/hooks"
import { derivativeAPI } from "~/utils/derivatives"
import type { DerivativeStorage } from "~/types/derivatives"

export default function DerivativeWorkerLimit(props: { storage: DerivativeStorage; busy: boolean; changed: () => void }) {
 const t=useT();const tx=(key:string)=>t(`derivatives.workers.${key}`)
 const [draft,setDraft]=createSignal<number>();const [saving,setSaving]=createSignal(false);const [error,setError]=createSignal("");const [saved,setSaved]=createSignal(false)
 const value=()=>draft()??props.storage.workers??1
 const save=async()=>{
  if(saving()||props.busy)return
  setSaving(true);setError("");setSaved(false)
  try {await derivativeAPI("/workers",{workers:value()});setDraft(undefined);setSaved(true);props.changed()}
  catch {setError(tx("failed"))}
  finally {setSaving(false)}
 }
 return <div class="dc-worker-control">
  <label>{t("derivatives.storage_stats.workers")}<div class="dc-worker-edit"><select aria-label={t("derivatives.storage_stats.workers")} value={value()} disabled={saving()||props.busy} onChange={event=>{setDraft(Number(event.currentTarget.value));setSaved(false);setError("")}}><option value={1}>1</option><option value={2}>2</option><option value={3}>3</option><option value={4}>4</option></select><button class="dc-button" disabled={saving()||props.busy||value()===(props.storage.workers??1)} onClick={()=>void save()}>{saving()?tx("saving"):tx("apply")}</button></div></label>
  <span class="dc-worker-active">{tx("active")}: {props.storage.active_workers??0}</span>
  <Show when={error()}><p class="dc-alert" role="alert">{error()}</p></Show>
  <Show when={saved()}><p class="dc-worker-saved" role="status">{tx("saved")}</p></Show>
 </div>
}
