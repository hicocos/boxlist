package derivatives

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"gorm.io/gorm"
)

type RuleRequest struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Path            string   `json:"path"`
	Recursive       bool     `json:"recursive"`
	Enabled         bool     `json:"enabled"`
	Profiles        []string `json:"profiles"`
	IntervalHours   int      `json:"interval_hours"`
	IntervalMinutes int      `json:"interval_minutes"`
	Excludes        []string `json:"excludes"`
}

func validateExcludes(excludes []string) (string, error) {
	if len(excludes) > 64 {
		return "", fmt.Errorf("too_many_excludes")
	}
	out := make([]string, 0, len(excludes))
	total := 0
	for _, p := range excludes {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		total += len(p)
		if len(p) > 512 || total > 8192 || strings.ContainsAny(p, "\\\x00\r\n") {
			return "", ErrInvalidPath
		}
		for _, part := range strings.Split(p, "/") {
			if part == ".." || part == "." {
				return "", ErrInvalidPath
			}
		}
		if _, e := doublestar.Match(p, ""); e != nil {
			return "", ErrInvalidPath
		}
		out = append(out, p)
	}
	return strings.Join(out, "\n"), nil
}
func excluded(root, p, excludes string) bool {
	rel := strings.TrimPrefix(p, strings.TrimSuffix(root, "/")+"/")
	for _, pattern := range strings.Split(excludes, "\n") {
		if pattern == "" {
			continue
		}
		candidate := rel
		if strings.HasPrefix(pattern, "/") {
			candidate = p
		}
		if candidate == strings.TrimSuffix(pattern, "/") || strings.HasPrefix(candidate, strings.TrimSuffix(pattern, "/")+"/") {
			return true
		}
		if ok, _ := doublestar.Match(pattern, candidate); ok {
			return true
		}
		if !strings.Contains(pattern, "/") {
			if ok, _ := doublestar.Match(pattern, path.Base(p)); ok {
				return true
			}
		}
	}
	return false
}
func (m *Manager) Rules() ([]Rule, error) {
	rows := []Rule{}
	e := m.db.Order("id").Find(&rows).Error
	return rows, e
}
func (m *Manager) SaveRule(ctx context.Context, userID uint, r RuleRequest) (Rule, error) {
	var rule Rule
	if m.ctx.Err() != nil {
		return rule, ErrClosed
	}
	if _, e := m.creator(ctx, userID); e != nil {
		return rule, e
	}
	p, e := canonicalPath(r.Path)
	if e != nil {
		return rule, e
	}
	profiles, e := normalizeProfiles(strings.Join(r.Profiles, ","))
	if e != nil {
		return rule, e
	}
	excludes, e := validateExcludes(r.Excludes)
	if e != nil {
		return rule, e
	}
	if len(r.Name) > 128 {
		return rule, fmt.Errorf("rule_name_too_long")
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = path.Base(p)
	}
	interval := r.IntervalMinutes
	if interval == 0 {
		if r.IntervalHours == 0 {
			interval = 360
		} else if r.IntervalHours < 0 || r.IntervalHours > 8760 {
			return rule, fmt.Errorf("invalid_rule_interval")
		} else {
			interval = r.IntervalHours * 60
		}
	}
	if interval < 15 || interval > 525600 {
		return rule, fmt.Errorf("invalid_rule_interval")
	}
	var id uint
	if r.ID != "" {
		id, e = parseID(r.ID)
		if e != nil {
			return rule, e
		}
	}
	if e = m.checkPermission(ctx, userID, p); e != nil {
		return rule, e
	}
	b, _, e := m.resolve(p)
	if e != nil {
		return rule, e
	}
	m.gate.Lock()
	defer m.gate.Unlock()
	e = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if id != 0 {
			if er := tx.First(&rule, id).Error; er != nil {
				return er
			}
		}
		if id == 0 {
			var n int64
			if er := tx.Model(&Rule{}).Count(&n).Error; er != nil {
				return er
			}
			if n >= m.cfg.MaxRules {
				return ErrLimit
			}
		}
		// Saving a changed rule is an explicit rebind. Existing queued jobs remain
		// bound to their original source and will pause if it no longer matches.
		rule.Name = name
		rule.Path = p
		rule.Recursive = r.Recursive
		rule.Profiles = profiles
		rule.Excludes = excludes
		rule.Enabled = r.Enabled
		rule.Creator = userID
		rule.StorageID = b.ID
		rule.Binding = b.Binding
		rule.IntervalMinutes = interval
		rule.PauseReason = ""
		if rule.Enabled {
			rule.NextRun = time.Now().Add(time.Duration(interval) * time.Minute)
		} else {
			rule.NextRun = time.Time{}
		}
		if id == 0 {
			return tx.Create(&rule).Error
		}
		return tx.Save(&rule).Error
	})
	if e == nil {
		m.signal()
	}
	return rule, e
}
func (m *Manager) DeleteRule(id string) error {
	n, e := parseID(id)
	if e != nil {
		return e
	}
	m.gate.Lock()
	defer m.gate.Unlock()
	// Deleting a rule does not remove its images; associated unfinished jobs are
	// paused explicitly rather than silently discarded or re-scoped.
	return m.db.Transaction(func(tx *gorm.DB) error {
		r := tx.Delete(&Rule{}, n)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Model(&Job{}).Where("rule_id = ? AND state IN ?", n, runnableStates).Updates(map[string]any{"state": "paused", "pause_reason": "rule_deleted"}).Error
	})
}
func (m *Manager) ScanRule(ctx context.Context, userID uint, id string) (Job, error) {
	if _, e := m.creator(ctx, userID); e != nil {
		return Job{}, e
	}
	n, e := parseID(id)
	if e != nil {
		return Job{}, e
	}
	return m.scanRule(ctx, n, false)
}
func (m *Manager) scanRule(ctx context.Context, id uint, automatic bool) (Job, error) {
	return m.scanRuleAt(ctx, id, automatic, time.Now())
}

func (m *Manager) scanRuleAt(ctx context.Context, id uint, automatic bool, now time.Time) (Job, error) {
	if m.ctx.Err() != nil {
		return Job{}, ErrClosed
	}
	if !m.cfg.Enabled {
		return Job{}, ErrDisabled
	}
	if e := ctx.Err(); e != nil {
		return Job{}, e
	}
	// Serialize with events and rule edits, including disabling/deleting a rule.
	// All validation here is local; no directory listing or download holds gate.
	m.gate.Lock()
	defer m.gate.Unlock()
	var r Rule
	if e := m.db.WithContext(ctx).First(&r, id).Error; e != nil {
		return Job{}, e
	}
	if automatic && (!r.Enabled || r.PauseReason != "") {
		return Job{}, ErrDisabled
	}
	if automatic && r.NextRun.After(now) {
		// A scheduler candidate may be stale after another scan or rule edit.
		return Job{}, ErrConflict
	}
	b, _, e := m.resolve(r.Path)
	if e == nil && (b.ID != r.StorageID || b.Binding != r.Binding) {
		e = ErrBinding
	}
	if e == nil {
		_, e = m.creator(ctx, r.Creator)
	}
	if e != nil {
		m.db.Model(&Rule{}).Where("id = ?", r.ID).Updates(map[string]any{"enabled": false, "pause_reason": ErrorCode(e), "last_state": "paused", "next_run": time.Time{}})
		return Job{}, e
	}
	j, e := m.submitRuleLocked(ctx, r, now)
	if e != nil {
		next := time.Time{}
		if r.Enabled {
			next = now.Add(15 * time.Minute)
		}
		m.db.Model(&Rule{}).Where("id = ?", r.ID).Updates(map[string]any{"last_state": ErrorCode(e), "next_run": next})
		return j, e
	}
	m.signal()
	j.Stats, e = m.stats(j.ID)
	return j, e
}

// submitRuleLocked keeps the job/root checkpoint and the consumed rule deadline
// in one transaction. NextRun is also the durable pending-follow-up marker: only
// a NEW job consumes it. An active-key hit cannot rescan completed directories,
// so leave a short retry instead of advancing LastRun/the full interval.
// The caller holds gate; calling submit here would recursively acquire it.
func (m *Manager) submitRuleLocked(ctx context.Context, r Rule, now time.Time) (Job, error) {
	var j Job
	p, e := canonicalPath(r.Path)
	if e != nil {
		return j, e
	}
	profiles, e := normalizeProfiles(r.Profiles)
	if e != nil {
		return j, e
	}
	if e = m.checkPermission(ctx, r.Creator, p); e != nil {
		return j, e
	}
	// Match submit's active identity, including the rule ID and excludes.
	fingerprint := digest(r.Binding, p, profiles, fmt.Sprint(r.Recursive, false, r.Creator, r.ID), r.Excludes)
	e = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("active_key = ?", fingerprint).First(&j).Error
		created := errors.Is(err, gorm.ErrRecordNotFound)
		if err != nil && !created {
			return err
		}
		if created {
			var count int64
			if err = tx.Model(&Job{}).Count(&count).Error; err != nil {
				return err
			}
			if count >= m.cfg.MaxJobs {
				return ErrLimit
			}
			j = Job{Path: p, Recursive: r.Recursive, Profiles: profiles, Creator: r.Creator, RuleID: r.ID, StorageID: r.StorageID, Binding: r.Binding, State: "queued", Excludes: r.Excludes, Fingerprint: fingerprint, ActiveKey: &fingerprint}
			if err = tx.Create(&j).Error; err != nil {
				return err
			}
			if err = tx.Create(&Item{JobID: j.ID, Path: p, PathKey: digest(p), Parent: p, Kind: "directory", State: "queued", Stage: "scan", Profiles: profiles}).Error; err != nil {
				return err
			}
		}
		next := time.Time{}
		if r.Enabled {
			next = now.Add(time.Minute)
			if created {
				next = now.Add(time.Duration(r.IntervalMinutes) * time.Minute)
			} else if r.NextRun.After(now) && r.NextRun.Before(next) {
				// An explicit duplicate must not postpone an earlier pending check.
				next = r.NextRun
			}
		}
		updates := map[string]any{"next_run": next, "last_state": j.State, "pause_reason": ""}
		if created {
			updates["last_run"] = now
		}
		return tx.Model(&Rule{}).Where("id = ?", r.ID).Updates(updates).Error
	})
	return j, e
}
func (m *Manager) schedule() {
	m.scheduleAt(time.Now())
}

func (m *Manager) scheduleAt(now time.Time) {
	if !m.cfg.Enabled {
		return
	}
	// Space protection does not consume attempts. It is auto-resumed only when
	// the same conservative reservation would now fit; manual pauses stay paused.
	if m.diskAvailable(workReserve) == nil {
		m.db.Model(&Job{}).Where("state = ? AND pause_reason = ?", "paused", "derivative_disk_protection").Updates(map[string]any{"state": "queued", "pause_reason": "", "error": ""})
	}
	var rules []Rule
	if e := m.db.Where("enabled = ? AND pause_reason = ? AND next_run <= ?", true, "", now).Order("next_run").Limit(20).Find(&rules).Error; e != nil {
		return
	}
	for _, r := range rules {
		if m.ctx.Err() != nil {
			return
		}
		_, _ = m.scanRuleAt(m.ctx, r.ID, true, now)
	}
}
func (m *Manager) event(parent string) {
	m.eventAt(parent, time.Now())
}

func (m *Manager) eventAt(parent string, now time.Time) {
	if !m.cfg.Enabled || m.ctx.Err() != nil {
		return
	}
	parent, e := canonicalPath(parent)
	if e != nil {
		return
	}
	// Persist before attempting submission. An event arriving during a scan waits
	// for its atomic enqueue/schedule update, then leaves its own pending check.
	// Loading under the same gate also prevents stale events rearming disabled or
	// rebound rules. Release it before scanRuleAt, which rechecks enabled/due.
	m.gate.Lock()
	var rules []Rule
	if e = m.db.Where("enabled = ? AND pause_reason = ?", true, "").Find(&rules).Error; e != nil {
		m.gate.Unlock()
		return
	}
	dueRules := make([]uint, 0, len(rules))
	for _, r := range rules {
		if (!r.Recursive && r.Path != parent) || !inside(r.Path, parent) || excluded(r.Path, parent, r.Excludes) {
			continue
		}
		// A check already due within a minute also covers this notification. Do
		// not pull it forward only to push it back on another active-key hit;
		// repeated events must not keep postponing a pending follow-up.
		if r.NextRun.After(now) && !r.NextRun.After(now.Add(time.Minute)) {
			continue
		}
		due := r.LastRun.Add(time.Minute)
		if due.Before(now) {
			due = now
		}
		if r.NextRun.IsZero() || r.NextRun.After(due) {
			if e = m.db.Model(&Rule{}).Where("id = ?", r.ID).Update("next_run", due).Error; e != nil {
				continue
			}
			r.NextRun = due
		}
		if !r.NextRun.After(now) {
			dueRules = append(dueRules, r.ID)
		}
	}
	m.gate.Unlock()
	for _, id := range dueRules {
		_, _ = m.scanRuleAt(m.ctx, id, true, now)
	}
	m.signal()
}
