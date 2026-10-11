import { batch, createEffect, createMemo, createSignal, For, on, onCleanup, onMount, Show, type JSX } from "solid-js"
import { FiArrowLeft, FiCheck, FiChevronRight, FiFolder, FiImage, FiPlus, FiRefreshCw, FiSearch, FiX } from "solid-icons/fi"
import { useManageTitle, useRouter, useT } from "~/hooks"
import { me } from "~/store"
import { UserMethods } from "~/types"
import type { DerivativeItem, DerivativeJob, DerivativeProfiles, DerivativeRule, DerivativeStats, DerivativeStorage } from "~/types/derivatives"
import { derivativeAPI, deleteDerivativeRule } from "~/utils/derivatives"
import { newDerivativeRule, validDerivativePath } from "~/utils/derivative-policy"
import { getFileSize } from "~/utils"
import { availableJobActions, budgetPercent, displayTime, jobFilters, jobMetrics, redactedReason, stateTone, type JobAction, type JobFilter, type TaskSummary } from "./derivative-console-policy"
import "./derivatives.css"
import DerivativeS3Storage from "./DerivativeS3Storage"
import DerivativeWorkerLimit from "./DerivativeWorkerLimit"
import DerivativeCleanup from "./DerivativeCleanup"

type Page<T> = { content: T[]; total: number }
type Confirmation = { title: string; description: string; paths?: string[]; action: () => Promise<unknown> }
const pageSize = 20
const statKeys: (keyof DerivativeStats)[] = ["eligible", "scanned_dirs", "failed_dirs", "existing_files", "retry_wait", "artifacts_ready", "artifacts_failed", "downloaded_bytes", "generated_bytes"]
function ConsoleDialog(props: { title: string; busy: boolean; close: () => void; children: JSX.Element }) {
  let dialog!: HTMLDialogElement
  onMount(() => dialog.showModal())
  onCleanup(() => dialog.close())
  return <dialog ref={dialog} class="dc-dialog" aria-labelledby="dc-dialog-title" onCancel={event => { event.preventDefault(); if (!props.busy) props.close() }}>
    <header class="dc-dialog-header"><h3 id="dc-dialog-title">{props.title}</h3><button class="dc-icon-button" type="button" aria-label="Close" disabled={props.busy} onClick={props.close}><FiX /></button></header>
    {props.children}
  </dialog>
}
const DerivativeManager = () => {
  const t = useT()
  const { searchParams, setSearchParams } = useRouter()
  const tx = (key: string) => t(`derivatives.console.${key}`)
  useManageTitle("derivatives.title")
  const tab = () => searchParams.tab === "rules" || searchParams.tab === "storage" ? searchParams.tab : "jobs"
  const selected = () => typeof searchParams.job === "string" ? searchParams.job : ""
  const [page, setPage] = createSignal(1)
  const [filter, setFilter] = createSignal<JobFilter>("")
  const [query, setQuery] = createSignal("")
  const [search, setSearch] = createSignal("")
  const [jobs, setJobs] = createSignal<Page<DerivativeJob>>({ content: [], total: 0 })
  const [summary, setSummary] = createSignal<TaskSummary>()
  const [job, setJob] = createSignal<DerivativeJob>()
  const [checked, setChecked] = createSignal<string[]>([])
  const [itemPage, setItemPage] = createSignal(1)
  const [state, setState] = createSignal("")
  const [itemQuery, setItemQuery] = createSignal("")
  const [itemSearch, setItemSearch] = createSignal("")
  const [items, setItems] = createSignal<Page<DerivativeItem>>({ content: [], total: 0 })
  const [rules, setRules] = createSignal<DerivativeRule[]>([])
  const [storage, setStorage] = createSignal<DerivativeStorage>()
  const [draft, setDraft] = createSignal<DerivativeRule>()
  const [creating, setCreating] = createSignal(false)
  const [newJob, setNewJob] = createSignal({ path: "", recursive: true, profiles: ["thumb", "reader"] as DerivativeProfiles, force: false })
  const [confirmation, setConfirmation] = createSignal<Confirmation>()
  const [reload, setReload] = createSignal(0)
  const [busy, setBusy] = createSignal(false)
  const [loading, setLoading] = createSignal(false)
  const [ready, setReady] = createSignal(false)
  const [itemsReady, setItemsReady] = createSignal(false)
  const [lastRefresh, setLastRefresh] = createSignal("")
  const [loadError, setLoadError] = createSignal("")
  const [actionError, setActionError] = createSignal("")
  const [formError, setFormError] = createSignal("")
  const [notice, setNotice] = createSignal("")
  const actionController = new AbortController()
  onCleanup(() => actionController.abort())
  const refresh = () => setReload(value => value + 1)
  const resetList = () => batch(() => { setPage(1); setChecked([]); setJobs({ content: [], total: 0 }); setReady(false) })
  const resetItems = () => batch(() => { setItemPage(1); setItems({ content: [], total: 0 }); setItemsReady(false) })
  const selectJob = (id: string | number) => setSearchParams({ job: String(id), tab: undefined })
  createEffect(on(selected, () => batch(() => {
    setJob(undefined); resetItems(); setState(""); setItemQuery(""); setItemSearch("")
  })))
  createEffect(on(query, value => {
    const timer = setTimeout(() => { if (value !== search()) batch(() => { resetList(); setSearch(value) }) }, 300)
    onCleanup(() => clearTimeout(timer))
  }))
  createEffect(on(itemQuery, value => {
    const timer = setTimeout(() => { if (value !== itemSearch()) batch(() => { resetItems(); setItemSearch(value) }) }, 300)
    onCleanup(() => clearTimeout(timer))
  }))
  const changeTab = (next: string) => batch(() => {
    setReady(false); setLoadError(""); setSearchParams({ tab: next === "jobs" ? undefined : next })
  })
  // One serial polling owner; navigation aborts all old reads. Hidden tabs pause,
  // failures back off, and mutations invalidate a generation before it can publish.
  createEffect(on([tab, page, filter, search, selected, itemPage, state, itemSearch, reload, busy], () => {
    const controller = new AbortController()
    let timer: ReturnType<typeof setTimeout> | undefined
    let failures = 0, stopped = false
    const snapshot = { tab: tab(), page: page(), filter: filter(), search: search(), id: selected(), itemPage: itemPage(), state: state(), itemSearch: itemSearch(), busy: busy() }
    onCleanup(() => { stopped = true; controller.abort(); clearTimeout(timer) })
    const poll = async () => {
      if (stopped || snapshot.busy) return
      if (!document.hidden) {
        setLoading(true)
        try {
          const space = await derivativeAPI<DerivativeStorage>("/storage", undefined, controller.signal)
          if (stopped) return
          setStorage(space)
          if (snapshot.tab === "jobs") {
            const counts = await derivativeAPI<TaskSummary>("/jobs/summary", undefined, controller.signal)
            const list = await derivativeAPI<Page<DerivativeJob>>(`/jobs?page=${snapshot.page}&per_page=${pageSize}&state=${encodeURIComponent(snapshot.filter)}&search=${encodeURIComponent(snapshot.search)}`, undefined, controller.signal)
            if (stopped) return
            if (snapshot.page > Math.max(1, Math.ceil(list.total / pageSize))) { setPage(Math.max(1, Math.ceil(list.total / pageSize))); return }
            batch(() => { setSummary(counts); setJobs({ content: list.content || [], total: list.total }); setReady(true) })
            setChecked(ids => ids.filter(id => list.content?.some(row => String(row.id) === id)))
            if (snapshot.id) {
              const detail = await derivativeAPI<DerivativeJob>(`/jobs/${encodeURIComponent(snapshot.id)}`, undefined, controller.signal)
              const entries = await derivativeAPI<Page<DerivativeItem>>(`/jobs/${encodeURIComponent(snapshot.id)}/items?page=${snapshot.itemPage}&per_page=${pageSize}&state=${encodeURIComponent(snapshot.state)}&search=${encodeURIComponent(snapshot.itemSearch)}`, undefined, controller.signal)
              if (stopped) return
              if (snapshot.itemPage > Math.max(1, Math.ceil(entries.total / pageSize))) { setItemPage(Math.max(1, Math.ceil(entries.total / pageSize))); return }
              batch(() => { setJob(detail); setItems({ content: entries.content || [], total: entries.total }); setItemsReady(true) })
            }
          } else if (snapshot.tab === "rules") {
            const result = await derivativeAPI<DerivativeRule[]>("/rules", undefined, controller.signal)
            if (stopped) return
            batch(() => { setRules(result || []); setReady(true) })
          } else setReady(true)
          failures = 0
          if (!stopped) batch(() => { setLoadError(""); setLastRefresh(new Date().toLocaleTimeString()) })
        } catch {
          failures++
          if (!stopped) setLoadError(t("derivatives.request_failed"))
        } finally { if (!stopped) setLoading(false) }
      }
      if (!stopped) timer = setTimeout(poll, Math.min(60000, (snapshot.tab === "jobs" ? 5000 : 15000) * Math.pow(2, failures)))
    }
    const visible = () => { if (!document.hidden) { clearTimeout(timer); refresh() } }
    document.addEventListener("visibilitychange", visible)
    onCleanup(() => document.removeEventListener("visibilitychange", visible))
    void poll()
  }))
  const run = async (action: () => Promise<unknown>, success = tx("saved")) => {
    if (busy()) return
    batch(() => { setBusy(true); setLoading(false); setActionError(""); setFormError(""); setNotice("") })
    try {
      const result = await action()
      if (!actionController.signal.aborted) { setConfirmation(undefined); setNotice(typeof result === "string" ? result : success); refresh() }
    } catch (error) {
      if (!actionController.signal.aborted) {
        const message = error instanceof Error ? redactedReason(error.message) : ""
        const friendly = message ? t(`derivatives.reasons.${message}`, undefined, message) : ""
        const result = `${t("derivatives.action_failed")}${friendly ? ` (${friendly})` : ""}`
        setActionError(result); setFormError(result); setConfirmation(undefined)
      }
    } finally { if (!actionController.signal.aborted) setBusy(false) }
  }
  const friendlyReason = (value?: string) => value ? t(`derivatives.reasons.${value}`, undefined, redactedReason(value)) : ""
  const actionLabel = (action: JobAction) => action === "delete" ? tx("delete_history") : t(`derivatives.${action === "cancel" ? "cancel_job" : action}`)
  const jobAction = (action: JobAction, targets: DerivativeJob[]) => {
    const eligible = targets.filter(row => availableJobActions(row).includes(action))
    if (!eligible.length || busy()) return
    // Snapshot IDs/paths at click time; polling never changes the confirmation scope.
    const execute = async () => {
      let done = 0
      const failed: string[] = []
      for (const target of eligible) {
        try {
          const result = await derivativeAPI<DerivativeJob>(`/jobs/${encodeURIComponent(target.id)}/${action}`, {}, actionController.signal)
          done++
          if (action === "rescan" && result?.id !== undefined && eligible.length === 1) selectJob(result.id)
          if (action === "delete" && selected() === String(target.id)) setSearchParams({ job: undefined })
        } catch { failed.push(`#${target.id}`) }
        if (actionController.signal.aborted) return
      }
      setChecked([])
      if (failed.length) { refresh(); throw new Error(`${tx("batch_partial")}: ${done}/${eligible.length}; ${failed.join(", ")}`) }
    }
    const success = `${actionLabel(action)} · ${eligible.length} ${tx("task_unit")}`
    if (action === "cancel" || action === "delete" || action === "rescan" || eligible.length > 1) {
      setConfirmation({ title: success, description: action === "delete" ? tx("delete_history_hint") : action === "cancel" ? t("derivatives.cancel_confirm") : action === "rescan" ? tx("rescan_hint") : tx(`action_${action}`), paths: eligible.map(row => `#${row.id} · ${row.path}`), action: () => run(execute, success) })
    } else void run(execute, success)
  }
  const selectedJobs = createMemo(() => jobs().content.filter(row => checked().includes(String(row.id))))
  const batchCount = (action: JobAction) => selectedJobs().filter(row => availableJobActions(row).includes(action)).length
  const Pager = (props: { page: number; total: number; change: (page: number) => void }) => <nav class="dc-pager" aria-label={t("derivatives.pagination")}>
    <span>{t("derivatives.total")} {props.total} · {props.page} / {Math.max(1, Math.ceil(props.total / pageSize))}</span><div class="derivative-actions">
      <button class="dc-button" disabled={props.page <= 1 || busy() || loading()} onClick={() => props.change(props.page - 1)}>{t("derivatives.previous")}</button>
      <button class="dc-button" disabled={props.page * pageSize >= props.total || busy() || loading()} onClick={() => props.change(props.page + 1)}>{t("derivatives.next")}</button>
    </div>
  </nav>
  const Badge = (props: { state: string }) => <span class={`dc-badge dc-tone-${stateTone(props.state)}`}><i />{t(`derivatives.states.${props.state}`, undefined, props.state)}</span>
  const Progress = (props: { job: DerivativeJob; compact?: boolean }) => {
    const metrics = () => jobMetrics(props.job)
    return <div class="dc-progress-block">
      <div class="dc-progress-caption"><span>{metrics().percent === undefined ? tx(metrics().failedDirs ? "scan_incomplete" : props.job.state === "cancelled" ? "stopped" : "discovering") : `${metrics().settled} / ${metrics().total}`}</span><strong>{metrics().percent === undefined ? "—" : `${metrics().percent}%`}</strong></div>
      <progress classList={{ "dc-indeterminate": metrics().percent === undefined }} max={100} value={metrics().percent ?? 0} aria-label={t("derivatives.file_progress")} />
      <Show when={!props.compact}><p class="dc-muted">{metrics().percent === undefined ? t("derivatives.scan_discovered") : tx("progress_hint")}</p></Show>
    </div>
  }
  const updateDraft = (value: Partial<DerivativeRule>) => setDraft(old => old ? { ...old, ...value } : old)
  const updateNew = (value: Partial<ReturnType<typeof newJob>>) => setNewJob(old => ({ ...old, ...value }))
  const createJob = (event: SubmitEvent) => {
    event.preventDefault()
    const request = newJob()
    if (!validDerivativePath(request.path)) { setFormError(t("derivatives.invalid_path")); return }
    void run(async () => {
      const result = await derivativeAPI<DerivativeJob>("/jobs", { ...request }, actionController.signal)
      if (actionController.signal.aborted) return
      setCreating(false); batch(() => { resetList(); setFilter(""); setQuery(""); setSearch("") }); selectJob(result.id)
    }, tx("created_job"))
  }
  const saveRule = (event: SubmitEvent) => {
    event.preventDefault()
    const rule = draft()
    if (!rule) return
    if (!validDerivativePath(rule.path)) { setFormError(t("derivatives.invalid_path")); return }
    if (!Number.isFinite(rule.interval_hours) || rule.interval_hours < 0.25 || rule.interval_hours > 8760) { setFormError(t("derivatives.invalid_interval")); return }
    void run(async () => {
      await derivativeAPI("/rules", { id: rule.id === undefined ? undefined : String(rule.id), name: rule.name, path: rule.path, recursive: rule.recursive,
        enabled: rule.enabled, profiles: rule.profiles, interval_minutes: Math.round(rule.interval_hours * 60), excludes: rule.excludes }, actionController.signal)
      if (!actionController.signal.aborted) setDraft(undefined)
    })
  }
  const ruleAction = (rule: DerivativeRule, action: "scan" | "delete" | "toggle") => {
    if (rule.id === undefined || busy()) return
    const execute = async () => {
      if (action === "delete") return deleteDerivativeRule(rule.id!, actionController.signal)
      if (action === "toggle") return derivativeAPI("/rules", { id: String(rule.id), name: rule.name, path: rule.path, recursive: rule.recursive, profiles: rule.profiles,
        interval_minutes: Math.round(rule.interval_hours * 60), excludes: rule.excludes, enabled: !rule.enabled }, actionController.signal)
      const result = await derivativeAPI<DerivativeJob>(`/rules/${encodeURIComponent(rule.id!)}/scan`, {}, actionController.signal)
      if (!actionController.signal.aborted && result?.id !== undefined) selectJob(result.id)
    }
    if (action === "delete" || action === "toggle") setConfirmation({ title: t(`derivatives.${action === "delete" ? "delete_rule" : rule.enabled ? "pause_rule" : "enable_rule"}`), description: action === "delete" ? t("derivatives.delete_rule_confirm") : rule.enabled ? t("derivatives.rule_pause_hint") : tx("enable_rule_hint"), paths: [rule.path], action: () => run(execute) })
    else void run(execute, tx("created_job"))
  }
  const cleanup = (mode: "temporary" | "stale") => setConfirmation({ title: t(`derivatives.cleanup_${mode}`), description: t(`derivatives.cleanup_${mode}_confirm`), action: () => run(async () => {
    const result = await derivativeAPI<{ files?: number; bytes?: number; removed_files?: number; removed_bytes?: number }>("/cleanup", { mode }, actionController.signal)
    return `${tx("cleanup_done")} · ${result?.files ?? result?.removed_files ?? 0} ${tx("cleanup_item_unit")} · ${getFileSize(result?.bytes ?? result?.removed_bytes ?? 0)}`
  }, tx("cleanup_done")) })
  const ProfileField = (props: { value: DerivativeProfiles; change: (value: DerivativeProfiles) => void }) => <label>{t("derivatives.profiles")}<select value={props.value.join(",")} onChange={event => props.change(event.currentTarget.value === "thumb" ? ["thumb"] : ["thumb", "reader"])}><option value="thumb,reader">{t("derivatives.both_profiles")}</option><option value="thumb">{t("derivatives.thumb_only")}</option></select></label>
  const healthOK = () => storage()?.enabled && storage()?.encoder_available && storage()?.status === "ok"
  return <section class="derivative-manager">
    <header class="dc-header"><div><div class="dc-eyebrow">{tx("workspace")}</div><h2>{tx("heading")}</h2><p class="dc-muted">{tx("subtitle")}</p></div><div class="derivative-actions"><button class="dc-icon-button" aria-label={t("derivatives.refresh")} disabled={busy() || loading()} onClick={refresh}><FiRefreshCw classList={{ "dc-spin": loading() }} /></button><button class="dc-button dc-primary" disabled={busy()} onClick={() => { setFormError(""); setCreating(true) }}><FiPlus />{tx("new_job")}</button></div></header>
    <div class="dc-nav-line"><nav class="dc-tabs" aria-label={t("derivatives.title")}><For each={["jobs", "rules", "storage"] as const}>{name => <button aria-pressed={tab() === name} disabled={busy()} onClick={() => changeTab(name)}>{t(`derivatives.${name}`)}</button>}</For></nav><div class="dc-sync"><span classList={{ "dc-health-dot": true, "dc-health-warning": !healthOK() }} /><button class="dc-text-button" onClick={() => changeTab("storage")}>{storage() ? tx(healthOK() ? "health_ok" : "health_attention") : tx("connecting")}</button><span>{loadError() ? tx("stale") : lastRefresh() ? `${tx("synced")} ${lastRefresh()}` : tx("connecting")}</span></div></div>
    <Show when={loadError()}><div class="dc-alert" role="alert"><span>{loadError()} {tx("stale_hint")}</span><button class="dc-button" onClick={refresh} disabled={loading() || busy()}>{t("derivatives.refresh")}</button></div></Show>
    <Show when={actionError() && !creating() && !draft()}><p role="alert" class="dc-alert">{actionError()}</p></Show>
    <Show when={notice()}><p role="status" class="dc-notice"><FiCheck />{notice()}<button class="dc-icon-button" aria-label={t("derivatives.close")} onClick={() => setNotice("")}><FiX /></button></p></Show>
    <Show when={tab() === "jobs"}>
      <Show when={summary()}>{counts => <div class="dc-summary"><For each={["active", "attention", "completed", "total"] as const}>{key => <button aria-pressed={filter() === (key === "total" ? "" : key)} onClick={() => batch(() => { resetList(); setFilter(key === "total" ? "" : key) })}><span>{tx(`count_${key}`)}</span><strong>{counts()[key]}</strong></button>}</For></div>}</Show>
      <div class="dc-workspace" classList={{ "dc-detail-open": !!selected() }}>
        <section class="dc-list-pane" aria-label={t("derivatives.jobs")} aria-busy={loading()}>
          <div class="dc-toolbar"><label class="dc-search"><FiSearch /><input type="search" aria-label={tx("search_jobs")} placeholder={tx("search_jobs")} maxLength={256} value={query()} onInput={event => setQuery(event.currentTarget.value)} /></label><label class="dc-filter-label"><span class="dc-sr-only">{t("derivatives.state_filter")}</span><select aria-label={tx("filter_jobs")} value={filter()} onChange={event => batch(() => { resetList(); setFilter(event.currentTarget.value as JobFilter) })}><For each={jobFilters}>{value => <option value={value}>{tx(`filter_${value || "all"}`)}</option>}</For></select></label></div>
          <Show when={ready() && jobs().content.length > 0}><div class="dc-list-meta"><label class="dc-check"><input type="checkbox" aria-label={tx("select_page")} disabled={busy() || loading()} checked={checked().length === jobs().content.length && checked().length > 0} ref={input => createEffect(() => { input.indeterminate = checked().length > 0 && checked().length < jobs().content.length })} onChange={event => setChecked(event.currentTarget.checked ? jobs().content.map(row => String(row.id)) : [])} />{tx("select_page")}</label><span>{jobs().total} {tx("task_unit")}</span></div></Show>
          <Show when={checked().length}><div class="dc-batch-bar"><strong>{tx("selected")} {checked().length}</strong><For each={["pause", "resume", "retry", "cancel", "delete"] as JobAction[]}>{action => <button class="dc-text-button" disabled={!batchCount(action) || busy() || loading()} onClick={() => jobAction(action, selectedJobs())}>{actionLabel(action)} ({batchCount(action)})</button>}</For><button class="dc-icon-button" aria-label={tx("clear_selection")} onClick={() => setChecked([])}><FiX /></button></div><p class="dc-batch-hint dc-muted">{tx("batch_hint")}</p></Show>
          <Show when={ready()} fallback={<div class="dc-skeleton" role="status">{tx("loading")}<span /><span /><span /></div>}>
            <Show when={jobs().content.length} fallback={<div class="dc-empty"><FiFolder /><h3>{tx(query() || filter() ? "no_matches" : "empty_title")}</h3><p>{tx(query() || filter() ? "no_matches_hint" : "empty_hint")}</p><button class="dc-button" onClick={() => query() || filter() ? batch(() => { setQuery(""); setSearch(""); setFilter(""); resetList() }) : setCreating(true)}>{tx(query() || filter() ? "clear_filters" : "new_job")}</button></div>}>
              <ul class="dc-job-rows"><For each={jobs().content.map(row => String(row.id))}>{id => {
                const row = () => jobs().content.find(entry => String(entry.id) === id)!
                return <li classList={{ "dc-job-row": true, "dc-row-selected": selected() === id }}><label class="dc-row-check"><input type="checkbox" aria-label={`${tx("select_task")} #${id}`} disabled={busy() || loading()} checked={checked().includes(id)} onChange={event => setChecked(ids => event.currentTarget.checked ? [...ids, id] : ids.filter(value => value !== id))} /></label><button class="dc-job-open" aria-pressed={selected() === id} onClick={() => selectJob(id)}><div class="dc-row-top"><span class="dc-task-id">#{id}</span><Badge state={row().state} /><Show when={row().rule_id}><span class="dc-source">{tx("automatic")}</span></Show></div><strong class="dc-row-path">{row().path}</strong><Progress job={row()} compact /><div class="dc-row-footer"><span>{tx("success")} {jobMetrics(row()).completed} · {tx("exceptions")} {jobMetrics(row()).failed + jobMetrics(row()).partial + jobMetrics(row()).failedDirs}</span><time>{displayTime(row().created_at)}</time></div></button><FiChevronRight class="dc-row-arrow" /></li>
              }}</For></ul>
            </Show>
            <Show when={jobs().total > 0}><Pager page={page()} total={jobs().total} change={value => { setChecked([]); setPage(value); setReady(false) }} /></Show>
          </Show>
        </section>
        <aside class="dc-detail-pane" aria-label={tx("task_details")}>
          <Show when={selected()} fallback={<div class="dc-detail-empty"><FiImage /><h3>{tx("choose_task")}</h3><p>{tx("choose_task_hint")}</p></div>}>
            <div class="dc-detail-nav"><button class="dc-text-button" onClick={() => setSearchParams({ job: undefined })}><FiArrowLeft />{tx("back_to_list")}</button><span class="dc-task-id">#{selected()}</span></div>
            <Show when={job()} fallback={<div class="dc-skeleton" role="status">{loadError() ? tx("detail_failed") : tx("loading")}<span /><span /></div>}>{current => <>
              <header class="dc-detail-header"><Badge state={current().state} /><h3>{current().path}</h3><p class="dc-muted">{t(current().recursive ? "derivatives.recursive" : "derivatives.current_only")} · {t(current().profiles?.includes("reader") ? "derivatives.both_profiles" : "derivatives.thumb_only")}<Show when={current().force}> · {t("derivatives.force")}</Show></p></header>
              <div class="dc-lifecycle"><span classList={{ "dc-step": true, "dc-step-done": current().scan_complete }}>{tx("step_scan")}</span><FiChevronRight /><span classList={{ "dc-step": true, "dc-step-done": jobMetrics(current()).percent === 100 }}>{tx("step_process")}</span><FiChevronRight /><span class="dc-step">{t(`derivatives.states.${current().state}`, undefined, current().state)}</span></div>
              <Progress job={current()} />
              <dl class="dc-primary-stats"><For each={["completed", "pending", "failed", "skipped"] as const}>{key => <div><dt>{tx(`metric_${key}`)}</dt><dd classList={{ "dc-number-danger": key === "failed" && jobMetrics(current()).failed + jobMetrics(current()).partial > 0 }}>{key === "failed" ? jobMetrics(current()).failed + jobMetrics(current()).partial : jobMetrics(current())[key]}</dd></div>}</For></dl>
              <div class="dc-live-work"><span>{tx("active_files")}: <strong>{jobMetrics(current()).active}</strong></span><span>{tx("discovered")}: <strong>{jobMetrics(current()).total}</strong></span><span>{tx("failed_dirs")}: <strong>{jobMetrics(current()).failedDirs}</strong></span></div>
              <Show when={current().error || current().pause_reason}><p class="dc-alert">{friendlyReason(current().error || current().pause_reason)}</p></Show>
              <div class="dc-detail-actions derivative-actions"><For each={availableJobActions(current())}>{action => <button classList={{ "dc-button": true, "dc-primary": action === "resume" || action === "retry", "dc-danger-button": action === "cancel" || action === "delete" }} disabled={busy()} title={action === "delete" ? tx("delete_history_hint") : tx(`action_${action}`)} onClick={() => jobAction(action, [current()])}>{actionLabel(action)}</button>}</For></div>
              <details class="dc-details"><summary>{tx("technical_stats")}</summary><dl class="derivative-stats"><For each={statKeys}>{key => <div><dt>{t(`derivatives.stats.${key}`)}</dt><dd>{key.endsWith("_bytes") ? getFileSize(current().stats?.[key] || 0) : current().stats?.[key] || 0}</dd></div>}</For></dl><p class="dc-muted">{t("derivatives.created")}: {displayTime(current().created_at)}<br />{t("derivatives.updated")}: {displayTime(current().updated_at)}</p><Show when={current().error || current().pause_reason}><pre>{redactedReason(current().error || current().pause_reason)}</pre></Show></details>
              <section class="dc-items-section"><div class="dc-section-heading"><h4>{t("derivatives.items")}</h4><span class="dc-muted">{items().total}</span></div><div class="dc-item-toolbar"><select aria-label={tx("filter_items")} value={state()} onChange={event => batch(() => { resetItems(); setState(event.currentTarget.value) })}><For each={["", "attention", "pending", "active", "skipped", "succeeded", "existing", "cancelled"]}>{value => <option value={value}>{value === "attention" ? tx("filter_attention") : value === "pending" ? tx("filter_pending") : value ? t(`derivatives.states.${value}`, undefined, value) : t("derivatives.all")}</option>}</For></select><input type="search" maxLength={256} value={itemQuery()} placeholder={tx("search_items")} aria-label={tx("search_items")} onInput={event => setItemQuery(event.currentTarget.value)} /></div>
                <Show when={itemsReady()} fallback={<p class="dc-muted" role="status">{tx("loading")}</p>}><Show when={items().content.length} fallback={<p class="dc-empty-items">{tx("no_items")}</p>}><ul class="dc-item-list"><For each={items().content.map(item => `${item.kind}:${item.path}`)}>{key => {
                  const item = () => items().content.find(entry => `${entry.kind}:${entry.path}` === key)!
                  return <li><details><summary><div class="dc-item-heading"><strong>{item().path.split("/").filter(Boolean).pop() || "/"}</strong><Badge state={item().state} /></div><span class="dc-item-subtitle">{item().kind === "directory" ? tx("directory") : t(`derivatives.console.stages.${item().stage}`, undefined, item().stage || "—")}<Show when={item().error}> · {friendlyReason(item().error)}</Show></span></summary><div class="dc-item-details"><p class="derivative-path">{item().path}</p><p class="dc-muted">{t("derivatives.attempts")}: {item().attempts || 0}</p><For each={item().result || []}>{result => <div class="dc-profile-result"><span>{tx(result.profile === "reader" ? "profile_reader" : "profile_thumb")}</span><Badge state={result.state} /><span>{getFileSize(result.bytes || 0)}</span><Show when={result.error}><p>{friendlyReason(result.error)}</p></Show></div>}</For><Show when={item().error}><pre>{redactedReason(item().error)}</pre></Show></div></details></li>
                }}</For></ul></Show><Show when={items().total > 0}><Pager page={itemPage()} total={items().total} change={value => { setItemPage(value); setItemsReady(false) }} /></Show></Show>
              </section>
            </>}</Show>
          </Show>
        </aside>
      </div>
    </Show>
    <Show when={tab() === "rules"}>
      <div class="dc-section-heading"><div><h3>{t("derivatives.rules")}</h3><p class="dc-muted">{t("derivatives.rule_pause_hint")}</p></div><button class="dc-button dc-primary" disabled={busy()} onClick={() => { setFormError(""); setDraft(newDerivativeRule()) }}><FiPlus />{t("derivatives.new_rule")}</button></div>
      <Show when={ready()} fallback={<div class="dc-skeleton" role="status">{tx("loading")}<span /><span /></div>}><Show when={rules().length} fallback={<div class="dc-empty"><FiRefreshCw /><h3>{tx("empty_rules")}</h3><p>{t("derivatives.rules_hint")}</p></div>}><div class="dc-rule-list"><For each={rules().map(rule => String(rule.id))}>{id => {
        const rule = () => rules().find(entry => String(entry.id) === id)!
        return <article class="dc-rule"><div class="dc-rule-main"><div class="dc-section-heading"><h4>{rule().name}</h4><span class={`dc-badge dc-tone-${rule().enabled ? "success" : "neutral"}`}><i />{t(rule().enabled ? "derivatives.enabled" : "derivatives.disabled")}</span></div><p class="derivative-path">{rule().path}</p><p class="dc-muted">{t(rule().recursive ? "derivatives.recursive" : "derivatives.current_only")} · {rule().interval_hours} {tx("hours")} · {t(rule().profiles?.includes("reader") ? "derivatives.both_profiles" : "derivatives.thumb_only")}</p><p class="dc-rule-times">{t("derivatives.last_scan")}: {displayTime(rule().last_scan_at)}<br />{t("derivatives.next_scan")}: {rule().enabled ? displayTime(rule().next_scan_at) : "—"}</p><Show when={rule().last_error || rule().pause_reason}><p class="dc-alert">{friendlyReason(rule().last_error || rule().pause_reason)}</p></Show><Show when={rule().excludes?.length}><details class="dc-details"><summary>{t("derivatives.excludes")}</summary><pre>{rule().excludes.join("\n")}</pre></details></Show></div><div class="derivative-actions"><button class="dc-button" disabled={busy()} onClick={() => ruleAction(rule(), "scan")}>{t("derivatives.scan_now")}</button><button class="dc-button" disabled={busy()} onClick={() => { setFormError(""); setDraft({ ...rule() }) }}>{t("derivatives.edit_rule")}</button><button class="dc-text-button" disabled={busy()} onClick={() => ruleAction(rule(), "toggle")}>{t(rule().enabled ? "derivatives.pause_rule" : "derivatives.enable_rule")}</button><button class="dc-text-button dc-danger-button" disabled={busy()} onClick={() => ruleAction(rule(), "delete")}>{t("derivatives.delete_rule")}</button></div></article>
      }}</For></div></Show></Show>
    </Show>
    <Show when={tab() === "storage"}><Show when={storage()}>{space => <div class="dc-storage">
      <DerivativeS3Storage changed={refresh} />
      <section class="dc-storage-overview"><div class="dc-section-heading"><h3>{tx("service_status")}</h3><span class={`dc-badge dc-tone-${healthOK() ? "success" : "warning"}`}><i />{tx(healthOK() ? "health_ok" : "health_attention")}</span></div><dl class="dc-service-stats"><div><dt>{tx("generation")}</dt><dd>{tx(space().enabled ? "on" : "off")}</dd></div><div><dt>{tx("encoder")}</dt><dd>{space().encoder_available ? space().encoder || "libvips" : tx("unavailable")}</dd></div><div><dt>{t("derivatives.storage_stats.queue_length")}</dt><dd>{space().queue_length}</dd></div></dl><DerivativeWorkerLimit storage={space()} busy={busy()} changed={refresh} /><Show when={!space().enabled || !space().encoder_available}><p class="dc-alert">{t("derivatives.encoder_warning")}</p></Show><Show when={!healthOK() && space().enabled && space().encoder_available}><p class="dc-alert">{t("derivatives.space_warning")}</p></Show><Show when={space().encoder_error}><pre>{redactedReason(space().encoder_error)}</pre></Show></section>
      <section class="dc-budget"><div class="dc-section-heading"><h3>{tx("space_usage")}</h3><Show when={space().budget_bytes === 0}><span class="dc-badge dc-tone-neutral">{tx("unlimited")}</span></Show></div><div class="dc-budget-value"><strong>{getFileSize(space().used_bytes + space().work_bytes)}</strong><Show when={space().budget_bytes > 0}><span>/ {getFileSize(space().budget_bytes)}</span></Show></div><Show when={space().budget_bytes > 0}><progress max={100} value={budgetPercent(space().used_bytes, space().work_bytes, space().budget_bytes) ?? 0} aria-label={tx("space_usage")} /></Show><dl class="derivative-stats"><For each={["used_bytes", "work_bytes", "free_bytes"] as const}>{key => <div><dt>{t(`derivatives.storage_stats.${key}`)}</dt><dd>{getFileSize(space()[key] || 0)}</dd></div>}</For><Show when={space().storage_target === "local"}><div><dt>{t("derivatives.storage_stats.min_free_bytes")}</dt><dd>{getFileSize(space().effective_min_free_bytes)}</dd></div></Show></dl><Show when={space().storage_target === "s3"}><p class="dc-muted">{tx("s3_disk_hint")}</p></Show></section>
      <DerivativeCleanup busy={busy()} clean={cleanup} navigate={changeTab} />
    </div>}</Show></Show>
    <Show when={creating()}><ConsoleDialog title={tx("new_job")} busy={busy()} close={() => setCreating(false)}><form class="derivative-form dc-dialog-body" onSubmit={createJob}><label>{t("derivatives.path")}<input autofocus required maxLength={2048} value={newJob().path} placeholder={tx("path_placeholder")} onInput={event => updateNew({ path: event.currentTarget.value })} /></label><ProfileField value={newJob().profiles} change={profiles => updateNew({ profiles })} /><label class="derivative-check"><input type="checkbox" checked={newJob().recursive} onChange={event => updateNew({ recursive: event.currentTarget.checked })} />{t("derivatives.recursive")}</label><label class="derivative-check"><input type="checkbox" checked={newJob().force} onChange={event => updateNew({ force: event.currentTarget.checked })} />{t("derivatives.force")}</label><p class="dc-form-note">{t(newJob().force ? "derivatives.force_hint" : "derivatives.generate_hint")}</p><details class="dc-details"><summary>{tx("scope_details")}</summary><p>{t("derivatives.scope_hint")}</p></details><Show when={formError()}><p role="alert" class="dc-alert">{formError()}</p></Show><div class="dc-dialog-footer"><button type="button" class="dc-button" disabled={busy()} onClick={() => setCreating(false)}>{t("derivatives.close")}</button><button type="submit" class="dc-button dc-primary" disabled={busy()}>{busy() ? tx("submitting") : t("derivatives.start")}</button></div></form></ConsoleDialog></Show>
    <Show when={draft()}>{rule => <ConsoleDialog title={t(rule().id === undefined ? "derivatives.new_rule" : "derivatives.edit_rule")} busy={busy()} close={() => setDraft(undefined)}><form class="derivative-form dc-dialog-body" onSubmit={saveRule}><label>{t("derivatives.name")}<input autofocus required maxLength={128} value={rule().name} onInput={event => updateDraft({ name: event.currentTarget.value })} /></label><label>{t("derivatives.path")}<input required value={rule().path} placeholder={tx("path_placeholder")} onInput={event => updateDraft({ path: event.currentTarget.value })} /></label><ProfileField value={rule().profiles} change={profiles => updateDraft({ profiles })} /><label>{t("derivatives.interval_hours")}<input type="number" min={0.25} max={8760} step={0.25} required value={rule().interval_hours} onInput={event => updateDraft({ interval_hours: Number(event.currentTarget.value) })} /></label><label class="derivative-check"><input type="checkbox" checked={rule().recursive} onChange={event => updateDraft({ recursive: event.currentTarget.checked })} />{t("derivatives.recursive")}</label><label>{t("derivatives.excludes")}<textarea rows={3} value={rule().excludes.join("\n")} onInput={event => updateDraft({ excludes: event.currentTarget.value.split("\n").filter(value => value.trim()) })} /></label><label class="derivative-check"><input type="checkbox" checked={rule().enabled} onChange={event => updateDraft({ enabled: event.currentTarget.checked })} />{t("derivatives.enable_rule")}</label><p class="dc-form-note">{rule().enabled ? tx("enable_rule_hint") : t("derivatives.rules_hint")}</p><Show when={formError()}><p role="alert" class="dc-alert">{formError()}</p></Show><div class="dc-dialog-footer"><button type="button" class="dc-button" disabled={busy()} onClick={() => setDraft(undefined)}>{t("derivatives.close")}</button><button type="submit" class="dc-button dc-primary" disabled={busy()}>{busy() ? tx("submitting") : t("derivatives.save")}</button></div></form></ConsoleDialog>}</Show>
    <Show when={confirmation()}>{confirm => <ConsoleDialog title={confirm().title} busy={busy()} close={() => setConfirmation(undefined)}><div class="dc-dialog-body"><p>{confirm().description}</p><Show when={confirm().paths?.length}><ul class="dc-confirm-targets"><For each={confirm().paths}>{path => <li>{path}</li>}</For></ul></Show><div class="dc-dialog-footer"><button class="dc-button" disabled={busy()} onClick={() => setConfirmation(undefined)}>{tx("go_back")}</button><button class="dc-button dc-primary" disabled={busy()} onClick={() => void confirm().action()}>{busy() ? tx("submitting") : tx("confirm")}</button></div></div></ConsoleDialog>}</Show>
  </section>
}
export default function Derivatives() {
  const t = useT()
  return <Show when={UserMethods.is_admin(me())} fallback={<p role="alert">{t("manage.not_admin")}</p>}><DerivativeManager /></Show>
}
