package derivatives

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
)

type SubmitRequest struct {
	Path           string   `json:"path"`
	Recursive      bool     `json:"recursive"`
	Profiles       []string `json:"profiles"`
	Force          bool     `json:"force"`
	IdempotencyKey string   `json:"idempotency_key"`
}

// Submit does no remote IO: even root-directory validation belongs to the
// persistent scan item, so slow/offline drivers cannot hold up the POST.
func (m *Manager) Submit(ctx context.Context, userID uint, r SubmitRequest) (Job, error) {
	return m.submit(ctx, JobRequest{Path: r.Path, Recursive: r.Recursive, Profiles: strings.Join(r.Profiles, ","), Force: r.Force, IdempotencyKey: r.IdempotencyKey, Creator: userID}, "")
}
func (m *Manager) submit(ctx context.Context, r JobRequest, excludes string) (Job, error) {
	var zero Job
	if m.ctx.Err() != nil {
		return zero, ErrClosed
	}
	if !m.cfg.Enabled {
		return zero, ErrDisabled
	}
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	p, err := canonicalPath(r.Path)
	if err != nil {
		return zero, err
	}
	profiles, err := normalizeProfiles(r.Profiles)
	if err != nil {
		return zero, err
	}
	if len(r.IdempotencyKey) > 200 {
		return zero, ErrConflict
	}
	if err = m.checkPermission(ctx, r.Creator, p); err != nil {
		return zero, err
	}
	s, _, err := m.resolve(p)
	if err != nil {
		return zero, err
	}
	fingerprint := digest(s.Binding, p, profiles, fmt.Sprint(r.Recursive, r.Force, r.Creator, r.RuleID), excludes)
	var idem *string
	if r.IdempotencyKey != "" {
		key := digest(fmt.Sprint(r.Creator), r.IdempotencyKey)
		idem = &key
	}
	m.gate.Lock()
	defer m.gate.Unlock()
	var job Job
	err = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if idem != nil {
			e := tx.Where("idempotency_hash = ?", *idem).First(&job).Error
			if e == nil {
				if job.Fingerprint != fingerprint {
					return ErrConflict
				}
				return nil
			}
			if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
		}
		e := tx.Where("active_key = ?", fingerprint).First(&job).Error
		if e == nil {
			return nil
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		var count int64
		if e = tx.Model(&Job{}).Count(&count).Error; e != nil {
			return e
		}
		if count >= m.cfg.MaxJobs {
			return ErrLimit
		}
		job = Job{Path: p, Recursive: r.Recursive, Profiles: profiles, Force: r.Force, Creator: r.Creator, RuleID: r.RuleID, StorageID: s.ID, Binding: s.Binding, State: "queued", Excludes: excludes, Fingerprint: fingerprint, ActiveKey: &fingerprint, IdempotencyHash: idem}
		if e = tx.Create(&job).Error; e != nil {
			return e
		}
		return tx.Create(&Item{JobID: job.ID, Path: p, PathKey: digest(p), Parent: p, Kind: "directory", State: "queued", Stage: "scan", Profiles: profiles}).Error
	})
	if err == nil {
		m.signal()
		job.Stats, err = m.stats(job.ID)
	}
	return job, err
}

func parseID(id string) (uint, error) {
	n, e := strconv.ParseUint(id, 10, 64)
	if e != nil || n == 0 || uint64(uint(n)) != n {
		return 0, fmt.Errorf("invalid derivative id")
	}
	return uint(n), nil
}
func (m *Manager) getJob(id uint) (Job, error) { var j Job; e := m.db.First(&j, id).Error; return j, e }
func (m *Manager) Job(id string) (Job, error) {
	n, e := parseID(id)
	if e != nil {
		return Job{}, e
	}
	j, e := m.getJob(n)
	if e == nil {
		j.Stats, e = m.stats(n)
	}
	return j, e
}

// Jobs preserves the original unfiltered list contract.
func (m *Manager) Jobs(page, perPage int) ([]Job, int64, error) {
	return m.JobsFiltered(page, perPage, "", "")
}

// JobsFiltered applies state/search to the whole job table before counting or
// pagination; only the bounded returned page receives item/profile statistics.
func (m *Manager) JobsFiltered(page, perPage int, state, search string) ([]Job, int64, error) {
	rows := []Job{}
	var count int64
	q, e := jobListQuery(m.db, state, search)
	if e != nil {
		return rows, 0, e
	}
	if e = q.Session(&gorm.Session{}).Count(&count).Error; e != nil {
		return rows, 0, e
	}
	off, limit := pageBounds(page, perPage)
	e = q.Session(&gorm.Session{}).Order("id DESC").Offset(off).Limit(limit).Find(&rows).Error
	if e != nil {
		return rows, count, e
	}
	for i := range rows {
		rows[i].Stats, e = m.stats(rows[i].ID)
		if e != nil {
			return rows, count, e
		}
	}
	return rows, count, nil
}

// Items preserves the original signature and accepts both exact states and the
// documented item groups. ItemsFiltered adds an optional literal search.
func (m *Manager) Items(id string, page, perPage int, state string) ([]Item, int64, error) {
	return m.ItemsFiltered(id, page, perPage, state, "")
}

func (m *Manager) ItemsFiltered(id string, page, perPage int, state, search string) ([]Item, int64, error) {
	rows := []Item{}
	var total int64
	n, e := parseID(id)
	if e != nil {
		return rows, 0, e
	}
	q, e := itemListQuery(m.db, n, state, search)
	if e != nil {
		return rows, 0, e
	}
	if _, e = m.getJob(n); e != nil {
		return rows, 0, e
	}
	if e = q.Session(&gorm.Session{}).Count(&total).Error; e != nil {
		return rows, 0, e
	}
	off, limit := pageBounds(page, perPage)
	e = q.Session(&gorm.Session{}).Preload("Result").Order("id").Offset(off).Limit(limit).Find(&rows).Error
	return rows, total, e
}

func (m *Manager) stats(id uint) (Stats, error) {
	var s Stats
	var groups []struct {
		Kind, State              string
		Eligible                 bool
		N, Downloaded, Generated int64
	}
	e := m.db.Model(&Item{}).Select("kind, state, eligible, count(*) AS n, COALESCE(sum(downloaded_bytes),0) AS downloaded, COALESCE(sum(generated_bytes),0) AS generated").Where("job_id = ?", id).Group("kind, state, eligible").Scan(&groups).Error
	if e != nil {
		return s, e
	}
	for _, g := range groups {
		s.DownloadedBytes += g.Downloaded
		s.GeneratedBytes += g.Generated
		if g.Kind == "directory" {
			if g.State == "succeeded" {
				s.ScannedDirs += g.N
			}
			if g.State == "failed" {
				s.FailedDirs += g.N
			}
			continue
		}
		s.Discovered += g.N
		if g.Eligible {
			s.Eligible += g.N
		}
		switch g.State {
		case "succeeded":
			s.CompletedFiles += g.N
		case "partial_failed":
			s.PartialFiles += g.N
		case "existing":
			s.ExistingFiles += g.N
			s.CompletedFiles += g.N
		case "skipped":
			s.SkippedFiles += g.N
		case "failed":
			s.FailedFiles += g.N
		case "queued":
			s.PendingFiles += g.N
		case "retry_wait":
			s.RetryWait += g.N
			s.PendingFiles += g.N
		case "active":
			s.ActiveFiles += g.N
		}
	}
	var pg []struct {
		State string
		N     int64
	}
	e = m.db.Model(&DerivativeItemProfile{}).Select("state, count(*) AS n").Where("item_id IN (?)", m.db.Model(&Item{}).Select("id").Where("job_id = ?", id)).Group("state").Scan(&pg).Error
	for _, g := range pg {
		switch g.State {
		case "ready", "existing":
			s.ArtifactsReady += g.N
		case "failed":
			s.ArtifactsFailed += g.N
		}
	}
	return s, e
}
func (m *Manager) Detail(id uint) (any, error) {
	j, e := m.getJob(id)
	if e != nil {
		return nil, e
	}
	j.Stats, e = m.stats(id)
	return j, e
}

func (m *Manager) Action(id, action string) error {
	n, e := parseID(id)
	if e != nil {
		return e
	}
	return m.Control(n, action)
}
func (m *Manager) Control(id uint, action string) error {
	m.gate.Lock()
	defer m.gate.Unlock()
	j, e := m.getJob(id)
	if e != nil {
		return e
	}
	switch action {
	case "pause":
		if !contains(activeStates, j.State) {
			return ErrConflict
		}
		// The worker retains its context and completes its current bounded file.
		e = m.db.Model(&Job{}).Where("id = ?", id).Updates(map[string]any{"state": "paused", "pause_reason": "manual"}).Error
	case "resume":
		if j.State != "paused" {
			return ErrConflict
		}
		e = m.db.Model(&Job{}).Where("id = ?", id).Updates(map[string]any{"state": "queued", "pause_reason": "", "error": ""}).Error
	case "cancel":
		if !contains(activeStates, j.State) && j.State != "cancelling" {
			return ErrConflict
		}
		e = m.db.Transaction(func(tx *gorm.DB) error {
			if er := tx.Model(&Item{}).Where("job_id = ? AND state IN ?", id, pendingStates).Updates(map[string]any{"state": "cancelled", "stage": "cancelled", "lease_until": time.Time{}}).Error; er != nil {
				return er
			}
			if er := tx.Model(&DerivativeItemProfile{}).Where("item_id IN (?) AND state IN (?)", tx.Model(&Item{}).Select("id").Where("job_id = ?", id), []string{"queued", "active", "retry_wait"}).Update("state", "cancelled").Error; er != nil {
				return er
			}
			return tx.Model(&Job{}).Where("id = ?", id).Updates(map[string]any{"state": "cancelled", "active_key": nil, "pause_reason": ""}).Error
		})
		if e == nil {
			for _, claim := range m.activeClaims {
				if claim.JobID == id {
					claim.Cancel()
				}
			}
		}
	case "retry", "retry_failed":
		if contains(runnableStates, j.State) || m.hasJobClaimLocked(id) {
			return ErrConflict
		}
		e = m.db.Transaction(func(tx *gorm.DB) error {
			var other int64
			if er := tx.Model(&Job{}).Where("active_key = ? AND id <> ?", j.Fingerprint, id).Count(&other).Error; er != nil {
				return er
			}
			if other > 0 {
				return ErrConflict
			}
			ids := tx.Model(&Item{}).Select("id").Where("job_id = ? AND state IN ?", id, []string{"failed", "partial_failed", "cancelled"})
			if er := tx.Model(&DerivativeItemProfile{}).Where("item_id IN (?) AND state IN ?", ids, []string{"failed", "cancelled", "retry_wait", "active"}).Updates(map[string]any{"state": "queued", "attempts": 0, "error": ""}).Error; er != nil {
				return er
			}
			if er := tx.Model(&Item{}).Where("job_id = ? AND state IN ?", id, []string{"failed", "partial_failed", "cancelled"}).Updates(map[string]any{"state": "queued", "attempts": 0, "error": "", "next_attempt": time.Time{}}).Error; er != nil {
				return er
			}
			return tx.Model(&Job{}).Where("id = ?", id).Updates(map[string]any{"state": "queued", "scan_complete": false, "error": "", "pause_reason": "", "active_key": j.Fingerprint}).Error
		})
	case "delete":
		if contains(activeStates, j.State) || m.hasJobClaimLocked(id) {
			return ErrConflict
		}
		e = m.db.Transaction(func(tx *gorm.DB) error {
			if er := tx.Where("item_id IN (?)", tx.Model(&Item{}).Select("id").Where("job_id = ?", id)).Delete(&DerivativeItemProfile{}).Error; er != nil {
				return er
			}
			if er := tx.Where("job_id = ?", id).Delete(&Item{}).Error; er != nil {
				return er
			}
			return tx.Delete(&Job{}, id).Error
		})
	default:
		return fmt.Errorf("invalid derivative action")
	}
	if e == nil {
		m.signal()
	}
	return e
}
func contains(values []string, s string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func (m *Manager) creator(ctx context.Context, id uint) (*model.User, error) {
	if id == 0 {
		return nil, ErrPermission
	}
	var u model.User
	if e := m.db.WithContext(ctx).First(&u, id).Error; e != nil {
		return nil, ErrPermission
	}
	if u.Disabled || !u.IsAdmin() {
		return nil, ErrPermission
	}
	return &u, nil
}
