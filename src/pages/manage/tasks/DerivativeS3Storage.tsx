import { createSignal, onMount, onCleanup, Show } from "solid-js"
import { FiCheck, FiCloud, FiSettings, FiX } from "solid-icons/fi"
import { useT } from "~/hooks"
import { getFileSize } from "~/utils"
import { derivativeAPI } from "~/utils/derivatives"
import { redactedReason } from "./derivative-console-policy"

interface S3Config {
 enabled: boolean; endpoint: string; bucket: string; region: string; prefix: string; path_style: boolean;
 cdn_url: string; public_cdn: boolean; access_key_configured?: boolean; secret_key_configured?: boolean;
 target_id?: number; object_count?: number; used_bytes?: number;
}
interface S3Draft extends S3Config { access_key: string; secret_key: string }
const defaults: S3Config = {enabled:false,endpoint:"",bucket:"",region:"us-east-1",prefix:"boxlist-previews/",path_style:true,cdn_url:"",public_cdn:false}

export default function DerivativeS3Storage(props: { changed: () => void }) {
 const t=useT();const tx=(key:string)=>t(`derivatives.s3.${key}`)
 const [config,setConfig]=createSignal<S3Config>();const [draft,setDraft]=createSignal<S3Draft>();
 const [loading,setLoading]=createSignal(true);const [busy,setBusy]=createSignal(false);const [error,setError]=createSignal("");const [message,setMessage]=createSignal("");const [tested,setTested]=createSignal(false)
 const controller=new AbortController();onCleanup(()=>controller.abort())
 let dialog!: HTMLDialogElement
 const errorText=(e:unknown)=>{const code=e instanceof Error?redactedReason(e.message):"";return t(`derivatives.reasons.${code}`,undefined,code||tx("failed"))}
 const load=async()=>{setLoading(true);setError("");try{setConfig(await derivativeAPI<S3Config>("/s3",undefined,controller.signal))}catch(e){setError(errorText(e))}finally{setLoading(false)}}
 onMount(()=>void load())
 const edit=()=>{setDraft({...defaults,...(config()?.target_id?config():{}),access_key:"",secret_key:""});setError("");setMessage("");setTested(false);dialog.showModal()}
 const change=(value:Partial<S3Draft>)=>{setDraft(old=>old?{...old,...value}:old);setTested(false);setMessage("")}
 const close=()=>{if(!busy()){dialog.close();setDraft(undefined);setError("");setMessage("")}}
 const request=async(save:boolean)=>{
  if(busy()||!draft())return
  setBusy(true);setError("");setMessage("")
  try{
   if(save){await derivativeAPI<S3Config>("/s3",draft(),controller.signal,65000);setMessage(tx("saved"));await load();props.changed();dialog.close();setDraft(undefined)}
   else{await derivativeAPI("/s3/test",draft(),controller.signal,65000);setTested(true);setMessage(tx("tested"))}
  }catch(e){setError(errorText(e));setTested(false)}finally{setBusy(false)}
 }
 return <section class="dc-s3-storage">
  <div class="dc-section-heading"><h3><FiCloud />{tx("title")}</h3><button class="dc-button dc-primary" disabled={loading()||busy()} onClick={edit}><FiSettings />{tx("configure")}</button></div>
  <Show when={!loading()} fallback={<p class="dc-muted">{t("derivatives.console.loading")}</p>}>
   <Show when={config()}><div class="dc-s3-current"><span class={`dc-badge dc-tone-${config()!.enabled?"active":"neutral"}`}>{config()!.enabled?tx("active"):tx("local")}</span><Show when={config()!.bucket}><strong>{config()!.bucket}</strong><span class="dc-muted">{config()!.prefix}</span></Show></div><p class="dc-muted">{config()!.enabled?tx(config()!.public_cdn?"mode_direct":"mode_proxy"):tx("local_hint")}</p><Show when={config()!.target_id}><dl class="derivative-stats"><div><dt>{tx("objects")}</dt><dd>{config()!.object_count||0}</dd></div><div><dt>{tx("bytes")}</dt><dd>{getFileSize(config()!.used_bytes||0)}</dd></div></dl></Show></Show>
   <Show when={error()&&!draft()}><p role="alert" class="dc-alert">{error()}<button class="dc-button" onClick={()=>void load()}>{t("derivatives.refresh")}</button></p></Show>
  </Show>
  <Show when={message()&&!draft()}><p role="status" class="dc-notice"><FiCheck />{message()}</p></Show>
  <dialog ref={dialog} class="dc-dialog" aria-labelledby="dc-s3-title" onCancel={event=>{event.preventDefault();close()}}>
   <header class="dc-dialog-header"><h3 id="dc-s3-title">{tx("title")}</h3><button class="dc-icon-button" type="button" disabled={busy()} aria-label={t("derivatives.close")} onClick={close}><FiX /></button></header>
   <Show when={draft()}>{d=><form class="derivative-form dc-dialog-body" onSubmit={event=>{event.preventDefault();void request(true)}}>
    <label class="derivative-check"><input type="checkbox" checked={d().enabled} disabled={busy()} onChange={e=>change({enabled:e.currentTarget.checked})} />{tx("enable")}</label>
    <label>{tx("endpoint")}<input type="url" required={d().enabled} disabled={busy()} placeholder="https://s3.example.com" value={d().endpoint} onInput={e=>change({endpoint:e.currentTarget.value})} /></label>
    <div class="dc-s3-fields"><label>{tx("bucket")}<input required={d().enabled} disabled={busy()} value={d().bucket} onInput={e=>change({bucket:e.currentTarget.value})} /></label><label>{tx("region")}<input required={d().enabled} disabled={busy()} value={d().region} onInput={e=>change({region:e.currentTarget.value})} /></label></div>
    <label>{tx("prefix")}<input disabled={busy()} value={d().prefix} onInput={e=>change({prefix:e.currentTarget.value})} /></label>
    <label>{tx("access_key")}<input type="password" autocomplete="off" disabled={busy()} placeholder={d().access_key_configured?tx("keep_secret"):""} value={d().access_key} onInput={e=>change({access_key:e.currentTarget.value})} /></label>
    <label>{tx("secret_key")}<input type="password" autocomplete="new-password" disabled={busy()} placeholder={d().secret_key_configured?tx("keep_secret"):""} value={d().secret_key} onInput={e=>change({secret_key:e.currentTarget.value})} /></label>
    <label class="derivative-check"><input type="checkbox" disabled={busy()} checked={d().path_style} onChange={e=>change({path_style:e.currentTarget.checked})} />{tx("path_style")}</label>
    <label>{tx("cdn_url")}<input type="url" disabled={busy()} placeholder="https://images.example.com" value={d().cdn_url} onInput={e=>change({cdn_url:e.currentTarget.value})} /></label>
    <label>{tx("read_mode")}<select aria-label={tx("read_mode")} disabled={busy()} value={d().public_cdn?"direct":"proxy"} onChange={e=>change({public_cdn:e.currentTarget.value==="direct"})}><option value="proxy">{tx("proxy")}</option><option value="direct">{tx("direct")}</option></select></label>
    <Show when={d().public_cdn}><p class="dc-alert" role="note">{tx("public_warning")}</p></Show>
    <Show when={error()}><p class="dc-alert" role="alert">{error()}</p></Show><Show when={message()}><p class="dc-notice" role="status">{message()}</p></Show>
    <div class="dc-dialog-footer"><button type="button" class="dc-button" disabled={busy()} onClick={close}>{t("derivatives.close")}</button><button type="button" class="dc-button" disabled={busy()||!d().endpoint||!d().bucket} onClick={()=>void request(false)}>{busy()?tx("working"):tx("test")}</button><button type="submit" class="dc-button dc-primary" disabled={busy()||(d().enabled&&!tested())}>{busy()?tx("working"):tx("save")}</button></div>
   </form>}</Show>
  </dialog>
 </section>
}
