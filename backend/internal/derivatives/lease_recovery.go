package derivatives

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Terminal state belongs to the manager, not the expired file/scan context. A
// bounded detached context also lets Close persist an interrupted claim.
func (m *Manager) workerStateContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(m.ctx), 5*time.Second)
}

// SQL failures must not exhaust the source/encoding retry budget. Typed driver
// errors cover SQLite/PostgreSQL without importing another driver; the narrow
// text fallback also handles wrapped SQLite busy errors and MySQL diagnostics.
func workerDatabaseError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, sql.ErrConnDone) || errors.Is(err, sql.ErrTxDone) || errors.Is(err, driver.ErrBadConn) {
		return true
	}
	var sqlite interface{ Code() int }
	var postgres interface{ SQLState() string }
	if errors.As(err, &sqlite) || errors.As(err, &postgres) {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, marker := range []string{
		"database is locked", "database table is locked", "database schema is locked",
		"sqlite_busy", "sqlite_locked", "sql: database is closed",
		"sql: transaction has already been committed or rolled back",
		"deadlock found when trying to get lock", "lock wait timeout exceeded",
	} {
		if strings.Contains(text, marker) {
			return true
		}
	}
	return false
}

func (m *Manager) recoverLeases() error {
	m.gate.Lock()
	defer m.gate.Unlock()
	return m.recoverLeasesLocked()
}

// Caller holds gate for the entire recovery/claim boundary. There must be one
// Manager per derivatives database/root. Every active row without an item-level
// context is an orphan, even with a future lease; expired live work is never
// stolen. Filter live IDs in SQL so they cannot hide orphans behind a batch limit.
func (m *Manager) recoverLeasesLocked() error {
	liveIDs := make([]uint, 0, len(m.activeClaims))
	for id := range m.activeClaims {
		liveIDs = append(liveIDs, id)
	}
	for {
		recovered := 0
		err := m.transaction(m.ctx, func(tx *gorm.DB) error {
			// A retry reruns the whole batch. Never carry rows/counts from a
			// rolled-back attempt into the next transaction.
			recovered = 0
			var items []Item
			q := tx.Where("state = ?", "active")
			if len(liveIDs) > 0 {
				q = q.Where("id NOT IN ?", liveIDs)
			}
			if e := q.Order("id").Limit(100).Find(&items).Error; e != nil {
				return e
			}
			for _, item := range items {
				var job Job
				if e := tx.First(&job, item.JobID).Error; e != nil {
					return e
				}
				state := "queued"
				if !contains(activeStates, job.State) {
					state = "cancelled" // Never revive cancelled/terminal jobs.
				}
				// Only the unfinished active profile's attempt is refundable.
				// Preserve published artifacts and committed per-profile failures.
				if e := tx.Model(&DerivativeItemProfile{}).Where("item_id = ? AND state = ?", item.ID, "active").Updates(map[string]any{
					"state": state, "error": "", "attempts": gorm.Expr("CASE WHEN attempts > 0 THEN attempts - 1 ELSE 0 END"),
				}).Error; e != nil {
					return e
				}
				if state == "cancelled" {
					if e := tx.Model(&DerivativeItemProfile{}).Where("item_id = ? AND state IN ?", item.ID, []string{"queued", "retry_wait"}).Update("state", "cancelled").Error; e != nil {
						return e
					}
				}
				if e := tx.Model(&Item{}).Where("id = ? AND state = ?", item.ID, "active").Updates(map[string]any{
					"state": state, "stage": "recovered", "error": "", "next_attempt": time.Time{}, "lease_until": time.Time{},
					"attempts": gorm.Expr("CASE WHEN attempts > 0 THEN attempts - 1 ELSE 0 END"),
				}).Error; e != nil {
					return e
				}
				updates := map[string]any{"state": "queued"}
				if item.Kind == "directory" {
					updates["scan_complete"] = false
				}
				if job.State == "cancelling" {
					var remaining int64
					if e := tx.Model(&Item{}).Where("job_id = ? AND state = ?", job.ID, "active").Count(&remaining).Error; e != nil {
						return e
					}
					if remaining == 0 && !m.hasJobClaimLocked(job.ID) {
						if e := tx.Model(&Job{}).Where("id = ? AND state = ?", job.ID, "cancelling").Updates(map[string]any{"state": "cancelled", "active_key": nil, "pause_reason": ""}).Error; e != nil {
							return e
						}
					}
				} else if e := tx.Model(&Job{}).Where("id = ? AND state IN ?", job.ID, runnableStates).Updates(updates).Error; e != nil {
					return e
				}
				recovered++
			}
			return nil
		})
		if err != nil || recovered < 100 {
			return err
		}
	}
}
