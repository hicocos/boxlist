package derivatives

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	internalnet "github.com/OpenListTeam/OpenList/v4/internal/net"
	log "github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ErrorCode is safe for API responses and persisted diagnostics: never return
// driver errors, which commonly contain signed download URLs or credentials.
func ErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if code, ok := S3ErrorCode(err); ok {
		return code
	}
	var enc *EncodeError
	if errors.As(err, &enc) {
		switch enc.Code {
		case "skipped_limit", "unsupported_format", "unsupported_animation", "long_image_requires_original", "invalid_image", "encoder_unavailable", "encode_failed", "encode_timeout", "encode_canceled", "invalid_profile":
			return enc.Code
		}
		return "encode_failed"
	}
	var he *sourceHTTPError
	if errors.As(err, &he) {
		return fmt.Sprintf("source_http_%d", he.Status)
	}
	var status internalnet.HttpStatusCodeError
	if errors.As(err, &status) {
		return fmt.Sprintf("source_http_%d", int(status))
	}
	switch {
	case errors.Is(err, ErrBackupInvalid):
		return "derivative_backup_invalid"
	case errors.Is(err, ErrBackupIntegrity):
		return "derivative_backup_integrity_failed"
	case errors.Is(err, ErrBackupFailed):
		return "derivative_backup_failed"
	case errors.Is(err, ErrBackupConflict):
		return "derivative_backup_conflict"
	case errors.Is(err, ErrBackupStorage):
		return "derivative_backup_storage_unavailable"
	case errors.Is(err, ErrDisabled):
		return "derivatives_disabled"
	case errors.Is(err, ErrClosed):
		return "derivatives_closed"
	case errors.Is(err, ErrInvalidPath):
		return "invalid_derivative_path"
	case errors.Is(err, ErrInvalidProfiles):
		return "invalid_derivative_profiles"
	case errors.Is(err, ErrPermission):
		return "creator_not_active_admin"
	case errors.Is(err, ErrBinding):
		return "storage_binding_changed"
	case errors.Is(err, ErrUnsupportedStorage):
		return "unsupported_derivative_storage"
	case errors.Is(err, ErrLimit):
		return "derivative_queue_limit"
	case errors.Is(err, ErrDisk), errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EDQUOT):
		return "derivative_disk_protection"
	case errors.Is(err, ErrSourceChanged):
		return "source_changed"
	case errors.Is(err, ErrConflict):
		return "derivative_request_conflict"
	case workerDatabaseError(err):
		return "derivative_database_error"
	case errors.Is(err, gorm.ErrRecordNotFound):
		return "derivative_not_found"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "source_timeout"
	case errs.IsNotFoundError(err):
		return "source_not_found"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "source_incomplete"
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return "source_network_error"
	}
	return "derivative_operation_failed"
}
func skipError(e error) bool {
	return contains([]string{"skipped_limit", "unsupported_format", "unsupported_animation", "long_image_requires_original", "invalid_image"}, ErrorCode(e))
}
func pauseError(e error) bool {
	return contains([]string{"derivative_disk_protection", "encoder_unavailable", "creator_not_active_admin", "storage_binding_changed", "unsupported_derivative_storage", "s3_configuration_invalid", "s3_credentials_required", "s3_key_unavailable", "s3_configuration_failed", "s3_target_unavailable"}, ErrorCode(e))
}
func retryable(e error) bool {
	if e == nil || skipError(e) || pauseError(e) || errors.Is(e, context.Canceled) || errs.IsNotFoundError(e) || errors.Is(e, ErrLimit) || errors.Is(e, ErrInvalidPath) {
		return false
	}
	var he *sourceHTTPError
	if errors.As(e, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status == http.StatusRequestTimeout || he.Status >= 500
	}
	var status internalnet.HttpStatusCodeError
	if errors.As(e, &status) {
		return status == 429 || status == 408 || status >= 500
	}
	// Driver APIs do not expose a universal status type. Known permission errors
	// are terminal, while opaque failures still have a strict three-attempt cap.
	text := strings.ToLower(e.Error())
	for _, s := range []string{"403", "401", "forbidden", "unauthorized", "permission denied", "invalid_source_url", "source_url_loops", "symlink", "invalid_image"} {
		if strings.Contains(text, s) {
			return false
		}
	}
	return true
}
func retryDelay(attempt int, e error) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 3 {
		attempt = 3
	}
	delay := (time.Second * 2 << uint(attempt-1)) + time.Duration(rand.IntN(1000))*time.Millisecond
	var he *sourceHTTPError
	if errors.As(e, &he) && he.After > delay {
		delay = he.After + time.Duration(rand.IntN(1000))*time.Millisecond
	}
	return delay
}

func (m *Manager) loop() {
	defer m.wg.Done()
	select {
	case <-m.ctx.Done():
		return
	case <-conf.StoragesLoadSignal():
	}
	m.encoderMu.Lock()
	if EncoderHealth() != nil {
		m.encoderError = "encoder_unavailable"
	} else {
		m.encoderError = ""
	}
	m.encoderMu.Unlock()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastSchedule := time.Time{}
	for {
		if m.ctx.Err() != nil {
			return
		}
		if time.Since(lastSchedule) >= 15*time.Second {
			m.schedule()
			lastSchedule = time.Now()
		}
		// Coalesce at most the bounded channel capacity; scan submission is local.
		for n := 0; n < 128; n++ {
			select {
			case parent := <-m.events:
				m.event(parent)
			default:
				n = 128
			}
		}
		if m.cfg.Enabled && m.inRunWindow(time.Now()) && m.dispatch() {
			continue
		}
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
	}
}

// A canceled context retains its item-level slot until work and persistence return.
// Every field and activeClaims access is protected by gate.
type workerClaim struct {
	JobID  uint
	Cancel context.CancelFunc
	Kind   string
}

type claimedWork struct {
	ctx    context.Context
	job    Job
	item   Item
	cancel context.CancelFunc
}

func (m *Manager) hasJobClaimLocked(id uint) bool {
	for _, claim := range m.activeClaims {
		if claim.JobID == id {
			return true
		}
	}
	return false
}

// step remains synchronous for tests; runtime uses the same admission via dispatch.
func (m *Manager) step() bool {
	m.gate.Lock()
	work, progressed := m.claimWorkLocked()
	m.gate.Unlock()
	if work == nil {
		return progressed
	}
	return m.runClaim(work)
}

func (m *Manager) dispatch() bool {
	m.gate.Lock()
	work, progressed := m.claimWorkLocked()
	if work != nil {
		go m.runClaim(work)
	}
	m.gate.Unlock()
	return progressed
}

// Caller holds gate through recovery, SQL claim, registration and wg.Add.
func (m *Manager) claimWorkLocked() (*claimedWork, bool) {
	if m.ctx.Err() != nil {
		return nil, false
	}
	if e := m.recoverLeasesLocked(); e != nil {
		workerStateError("recover", 0, 0, e)
		return nil, false
	}
	limit := m.workerLimit
	if limit < 1 {
		limit = 1
	} else if limit > 4 {
		limit = 4
	}
	if len(m.activeClaims) >= limit {
		return nil, false // Lowering concurrency drains without interruption.
	}
	activeFiles, scanActive := 0, false
	liveIDs := make([]uint, 0, len(m.activeClaims))
	for id, claim := range m.activeClaims {
		liveIDs = append(liveIDs, id)
		if claim.Kind == "directory" {
			scanActive = true
		} else if claim.Kind == "file" {
			activeFiles++
		}
	}
	var jobs []Job
	e := m.db.WithContext(m.ctx).Where("state IN ?", runnableStates).Order("updated_at, id").Limit(32).Find(&jobs).Error
	if e != nil || len(jobs) == 0 {
		if e != nil {
			workerStateError("list_jobs", 0, 0, e)
		}
		return nil, false
	}
	var job Job
	var item Item
	found := false
	for _, j := range jobs {
		item = Item{}
		q := m.db.WithContext(m.ctx).Where("job_id = ? AND state IN ? AND (state = ? OR next_attempt <= ?)", j.ID, []string{"queued", "retry_wait"}, "queued", time.Now())
		if scanActive {
			q = q.Where("kind <> ?", "directory") // Only one global remote scan.
		}
		if len(liveIDs) > 0 {
			q = q.Where("id NOT IN ?", liveIDs)
		}
		order := "kind DESC, id"
		if m.fileTurn {
			order = "kind ASC, id"
		}
		er := q.Order(order).First(&item).Error
		if er == nil {
			job = j
			found = true
			break
		}
		if errors.Is(er, gorm.ErrRecordNotFound) {
			er = m.finalize(j)
		}
		if er != nil {
			workerStateError("select_or_finalize", j.ID, 0, er)
			return nil, false
		}
	}
	if !found {
		return nil, false
	}
	if item.Kind == "file" {
		reason := ""
		if m.encoderErrorSafe() != "" {
			reason = "encoder_unavailable"
		} else if err := m.diskAvailable(workReserve * int64(activeFiles+1)); err != nil {
			reason = ErrorCode(err)
		}
		if reason != "" {
			e = m.pauseJob(job.ID, reason)
			if e != nil {
				workerStateError("pause", job.ID, item.ID, e)
			}
			return nil, e == nil
		}
	}
	timeout := m.cfg.FileTimeout
	if item.Kind == "directory" {
		timeout = m.cfg.ScanTimeout
	}
	ctx, cancel := context.WithTimeout(scanContext(m.ctx), timeout)
	e = m.transaction(ctx, func(tx *gorm.DB) error {
		r := tx.Model(&Item{}).Where("id = ? AND state IN ?", item.ID, []string{"queued", "retry_wait"}).Updates(map[string]any{"state": "active", "attempts": gorm.Expr("attempts + 1"), "lease_until": time.Now().Add(timeout + time.Minute)})
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrConflict
		}
		state := "processing"
		if item.Kind == "directory" {
			state = "scanning"
		}
		r = tx.Model(&Job{}).Where("id = ? AND state IN ?", job.ID, runnableStates).Update("state", state)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return ErrConflict
		}
		return nil
	})
	if e != nil {
		cancel()
		workerStateError("claim", job.ID, item.ID, e)
		return nil, false
	}
	item.Attempts++
	item.State = "active"
	m.activeClaims[item.ID] = workerClaim{JobID: job.ID, Cancel: cancel, Kind: item.Kind}
	m.fileTurn = !m.fileTurn
	// Serialized with Close's cancellation before its wg.Wait.
	m.wg.Add(1)
	return &claimedWork{ctx: ctx, job: job, item: item, cancel: cancel}, true
}

func (m *Manager) runClaim(work *claimedWork) (ok bool) {
	defer m.wg.Done()
	job, item := work.job, work.item
	var e error
	if item.Kind == "directory" {
		e = m.scan(work.ctx, job, item)
	} else {
		e = m.processFile(work.ctx, job, item)
	}
	work.cancel()
	m.gate.Lock()
	defer m.gate.Unlock()
	defer func() {
		// Returned SQL failures become orphans recoverable on the next tick,
		// without waiting for unrelated live contexts or a future lease.
		delete(m.activeClaims, item.ID)
		if ok {
			m.signal()
		}
	}()
	if e != nil {
		if er := m.failItem(job, item, e); er != nil {
			workerStateError("fail_item", job.ID, item.ID, er)
			return false // Let the loop back off instead of spinning on local SQL.
		}
	}
	if e = m.finalize(job); e != nil {
		workerStateError("finalize", job.ID, item.ID, e)
		return false
	}
	return true
}

// Log identifiers and safe codes, never raw source errors or signed URLs.
func workerStateError(operation string, jobID, itemID uint, e error) {
	log.WithFields(log.Fields{"operation": operation, "job_id": jobID, "item_id": itemID, "error_code": ErrorCode(e)}).
		Warn("derivative worker SQL state transition failed; will retry locally")
}

func (m *Manager) pauseJob(id uint, reason string) error {
	ctx, cancel := m.workerStateContext()
	defer cancel()
	return m.transaction(ctx, func(tx *gorm.DB) error {
		return tx.Model(&Job{}).Where("id = ? AND state IN ?", id, runnableStates).Updates(map[string]any{"state": "paused", "pause_reason": reason, "error": reason}).Error
	})
}

// Caller holds gate. Retry only the local transaction, never source/encoder work.
func (m *Manager) failItem(j Job, item Item, e error) error {
	ctx, cancel := m.workerStateContext()
	defer cancel()
	shutdown := m.ctx.Err() != nil
	databaseFailure := workerDatabaseError(e)
	return m.transaction(ctx, func(tx *gorm.DB) error {
		var current Job
		if er := tx.First(&current, j.ID).Error; er != nil {
			return er
		}
		if current.State == "cancelled" || current.State == "cancelling" {
			return nil
		}
		var active Item
		if er := tx.First(&active, item.ID).Error; er != nil {
			return er
		}
		if active.JobID != j.ID || active.State != "active" {
			return nil // Repeated completion must not refund attempts twice.
		}
		state, code, next := "failed", ErrorCode(e), time.Time{}
		switch {
		case shutdown:
			state, code = "queued", ""
		case databaseFailure:
			state, code = "retry_wait", "derivative_database_error"
			next = time.Now().Add(2 * time.Second)
		case pauseError(e):
			state = "queued"
			if er := tx.Model(&Job{}).Where("id = ? AND state IN ?", j.ID, runnableStates).Updates(map[string]any{"state": "paused", "pause_reason": code, "error": code}).Error; er != nil {
				return er
			}
		case skipError(e):
			state = "skipped"
		case retryable(e) && active.Attempts < 3:
			state = "retry_wait"
			next = time.Now().Add(retryDelay(active.Attempts, e))
		}
		profileState := state
		if active.Kind == "file" && state == "failed" {
			var ready int64
			if er := tx.Model(&DerivativeItemProfile{}).Where("item_id = ? AND state IN ?", item.ID, []string{"ready", "existing"}).Count(&ready).Error; er != nil {
				return er
			}
			if ready > 0 {
				state = "partial_failed"
			}
		}
		updates := map[string]any{"state": state, "error": code, "next_attempt": next, "lease_until": time.Time{}}
		if shutdown || databaseFailure || pauseError(e) {
			updates["attempts"] = gorm.Expr("CASE WHEN attempts > 0 THEN attempts - 1 ELSE 0 END")
		}
		r := tx.Model(&Item{}).Where("id = ? AND state = ?", item.ID, "active").Updates(updates)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected != 1 {
			return nil
		}
		if active.Kind == "file" {
			if shutdown || databaseFailure {
				// Only the interrupted active profile owns a refundable attempt.
				// Committed ready/failed/retry_wait profiles retain their results.
				return tx.Model(&DerivativeItemProfile{}).Where("item_id = ? AND state = ?", item.ID, "active").Updates(map[string]any{
					"state": profileState, "error": code, "attempts": gorm.Expr("CASE WHEN attempts > 0 THEN attempts - 1 ELSE 0 END"),
				}).Error
			}
			return tx.Model(&DerivativeItemProfile{}).Where("item_id = ? AND state IN ?", item.ID, pendingStates).Updates(map[string]any{"state": profileState, "error": code}).Error
		}
		return nil
	})
}

func (m *Manager) finalize(j Job) error {
	ctx, cancel := m.workerStateContext()
	defer cancel()
	return m.transaction(ctx, func(tx *gorm.DB) error {
		var current Job
		if e := tx.First(&current, j.ID).Error; e != nil {
			return e
		}
		if !contains(runnableStates, current.State) {
			return nil
		}
		var pendingDirs, pending int64
		if e := tx.Model(&Item{}).Where("job_id = ? AND kind = ? AND state IN ?", j.ID, "directory", pendingStates).Count(&pendingDirs).Error; e != nil {
			return e
		}
		if e := tx.Model(&Item{}).Where("job_id = ? AND state IN ?", j.ID, pendingStates).Count(&pending).Error; e != nil {
			return e
		}
		if pending > 0 {
			if pendingDirs == 0 && !current.ScanComplete {
				return tx.Model(&Job{}).Where("id = ? AND state IN ?", j.ID, runnableStates).Update("scan_complete", true).Error
			}
			return nil
		}
		var failed int64
		if e := tx.Model(&Item{}).Where("job_id = ? AND state IN ?", j.ID, []string{"failed", "partial_failed"}).Count(&failed).Error; e != nil {
			return e
		}
		state := "completed"
		if failed > 0 {
			state = "partial_failed"
		}
		if e := tx.Model(&Job{}).Where("id = ? AND state IN ?", j.ID, runnableStates).Updates(map[string]any{"state": state, "active_key": nil, "scan_complete": true}).Error; e != nil {
			return e
		}
		if current.RuleID != 0 {
			return tx.Model(&Rule{}).Where("id = ?", current.RuleID).Update("last_state", state).Error
		}
		return nil
	})
}
