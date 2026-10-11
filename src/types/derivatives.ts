export interface Derivative {
  state: "ready"
  url: string
  width: number
  height: number
  bytes: number
  version: string | number
}
export interface Derivatives {
  thumb?: Derivative
  reader?: Derivative
}
export type DerivativeProfiles = ("thumb" | "reader")[]
export interface DerivativeStats {
  discovered: number
  eligible: number
  scanned_dirs: number
  failed_dirs: number
  completed_files: number
  partial_files: number
  existing_files: number
  skipped_files: number
  failed_files: number
  pending_files: number
  retry_wait: number
  active_files: number
  downloaded_bytes: number
  generated_bytes: number
  artifacts_ready: number
  artifacts_failed: number
}
export interface DerivativeJob {
  id: string | number
  path: string
  state: string
  recursive: boolean
  profiles: DerivativeProfiles
  force?: boolean
  rule_id?: number
  pause_reason?: string
  scan_complete: boolean
  error?: string
  created_at: string
  updated_at: string
  stats?: Partial<DerivativeStats>
}
export interface DerivativeItem {
  path: string
  state: string
  error?: string
  stage?: string
  attempts?: number
  kind?: string
  result?: { profile: string; state: string; error?: string; bytes: number }[]
}
export interface DerivativeRule {
  id?: string | number
  name: string
  path: string
  recursive: boolean
  profiles: DerivativeProfiles
  enabled: boolean
  interval_hours: number
  excludes: string[]
  interval_minutes?: number
  last_scan_at?: string
  next_scan_at?: string
  last_error?: string
  last_state?: string
  error?: string
  last_result?: string
  pause_reason?: string
}
export interface DerivativeStorage {
  enabled: boolean
  encoder_available?: boolean
  encoder?: string
  workers?: number
  active_workers?: number
  min_workers?: number
  max_workers?: number
  encoder_error?: string
  used_bytes: number
  work_bytes: number
  budget_bytes: number
  free_bytes: number
  min_free_bytes: number
  effective_min_free_bytes: number
  storage_target: "local" | "s3"
  status: string
  queue_length: number
}
