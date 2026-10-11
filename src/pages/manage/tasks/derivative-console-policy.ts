import type { DerivativeJob } from "../../../types/derivatives"
import { derivativeJobActive, derivativeProgress, terminalDerivativeJob } from "../../../utils/derivative-policy"

export type JobFilter = "" | "active" | "paused" | "attention" | "completed" | "cancelled"
export interface TaskSummary { total: number; active: number; paused: number; attention: number; completed: number; cancelled: number }
export type JobAction = "pause" | "resume" | "cancel" | "retry" | "rescan" | "delete"
export const jobFilters: JobFilter[] = ["", "active", "paused", "attention", "completed", "cancelled"]
export const numericStat = (value: unknown) => typeof value === "number" && Number.isFinite(value) ? Math.max(0, value) : 0
export function jobMetrics(job: DerivativeJob) {
  const s = job.stats || {}
  const completed = numericStat(s.completed_files)
  const partial = numericStat(s.partial_files)
  const failed = numericStat(s.failed_files)
  const skipped = numericStat(s.skipped_files)
  return { total: numericStat(s.discovered), completed, partial, failed, skipped,
    settled: completed + partial + failed + skipped,
    pending: numericStat(s.pending_files), active: numericStat(s.active_files),
    retry: numericStat(s.retry_wait), failedDirs: numericStat(s.failed_dirs),
    percent: derivativeProgress(job), coverage: job.scan_complete && !numericStat(s.failed_dirs) }
}
/** State-valid controls only; a clean completed job has nothing to retry. */
export function availableJobActions(job: DerivativeJob): JobAction[] {
  if (["pausing", "cancelling"].includes(job.state)) return []
  if (derivativeJobActive(job.state)) return ["pause", "cancel"]
  if (job.state === "paused") return ["resume", "cancel"]
  if (!terminalDerivativeJob(job.state)) return []
  const m = jobMetrics(job)
  const retry = m.failed + m.partial + m.failedDirs > 0 || ["failed", "partial_failed", "completed_with_errors", "cancelled", "canceled"].includes(job.state)
  return [...(retry ? ["retry" as const] : []), "rescan", "delete"]
}
export function stateTone(state: string): "success" | "danger" | "warning" | "active" | "neutral" {
  if (["completed", "succeeded", "existing", "ready"].includes(state)) return "success"
  if (["failed", "partial_failed", "completed_with_errors"].includes(state)) return "danger"
  if (["paused", "pausing", "skipped", "retry_wait"].includes(state)) return "warning"
  if (derivativeJobActive(state) || state === "active") return "active"
  return "neutral"
}
export function redactedReason(value?: string): string {
  return (value || "").replace(/https?:\/\/\S+/gi, "[URL]").replace(/((?:token|password|authorization|signature|sign)\s*[:=]\s*)[^\s,;]+/gi, "$1[redacted]")
}
export function displayTime(value?: string): string {
  if (!value || value.startsWith("0001-")) return "—"
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : "—"
}
export function budgetPercent(used: number, work: number, budget: number): number | undefined {
  if (!Number.isFinite(budget) || budget <= 0) return
  return Math.min(100, Math.max(0, (numericStat(used) + numericStat(work)) / budget * 100))
}
