package derivatives

import (
	"context"
	"path"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func supportedExtension(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".jpg", ".jpeg", ".png", ".webp":
		return true
	}
	return false
}
func (m *Manager) scan(ctx context.Context, j Job, item Item) error {
	if err := waitDerivativePace(ctx, time.Duration(m.cfg.ScanIntervalMillis)*time.Millisecond); err != nil {
		return err
	}
	b, actual, e := m.validateJob(ctx, j, item.Path)
	if e != nil {
		return e
	}
	// op.List is the driver's complete listing contract: drivers themselves
	// exhaust numeric/cursor pagination. Never slice this result to a first page.
	objs, e := op.List(scanContext(ctx), b.Driver, actual, model.ListArgs{ReqPath: item.Path, Refresh: true, SkipHook: true})
	if e != nil {
		return e
	}
	if int64(len(objs)) > m.cfg.MaxItems {
		return ErrLimit
	}
	rows, e := m.storages(ctx)
	if e != nil {
		return e
	}
	present := map[string]bool{}
	entries := []Item{}
	sources := []DerivativeSource{}
	for _, o := range objs {
		if e = ctx.Err(); e != nil {
			return e
		}
		if o == nil || !validName(o.GetName()) {
			return ErrInvalidPath
		}
		if present[o.GetName()] {
			continue
		}
		present[o.GetName()] = true
		p := path.Join(item.Path, o.GetName())
		kind := "file"
		if o.IsDir() {
			kind = "directory"
		}
		next := Item{JobID: j.ID, PathKey: digest(p), Path: p, Parent: item.Path, Kind: kind, State: "queued", Stage: "scan", Profiles: j.Profiles, Depth: item.Depth + 1, Revision: Revision(o)}
		s, er := storageForPath(rows, p)
		if er != nil || s.ID != j.StorageID || Binding(s) != j.Binding {
			next.State = "skipped"
			next.Error = "cross_mount_or_unsupported_storage"
		} else if er = checkLocalPath(b, strings.TrimPrefix(p, b.Storage.MountPath)); er != nil {
			next.State = "skipped"
			next.Error = "source_symlink_or_unavailable"
		} else if excluded(j.Path, p, j.Excludes) {
			next.State = "skipped"
			next.Error = "rule_excluded"
		} else if o.IsDir() {
			if !j.Recursive {
				next.State = "skipped"
				next.Error = "non_recursive"
			} else if next.Depth > m.cfg.MaxDepth {
				next.State = "failed"
				next.Error = "scan_depth_limit"
			}
		} else {
			sources = append(sources, sourceFromObject(b.Storage, p, o))
			if !supportedExtension(o.GetName()) {
				next.State = "skipped"
				next.Error = "unsupported_format"
			} else if o.GetSize() > maxDownload {
				next.State = "skipped"
				next.Error = "skipped_limit"
			} else {
				next.Eligible = true
				next.Stage = "queued"
			}
		}
		entries = append(entries, next)
	}
	// Each committed batch is a durable checkpoint. On restart the directory is
	// listed again, and the unique (job,path) key makes re-discovery idempotent.
	for start := 0; start < len(entries); start += 100 {
		if e = ctx.Err(); e != nil {
			return e
		}
		end := start + 100
		if end > len(entries) {
			end = len(entries)
		}
		batch := entries[start:end]
		m.gate.Lock()
		e = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var current Job
			er := tx.First(&current, j.ID).Error
			if er != nil {
				return er
			}
			if current.State == "cancelled" {
				return context.Canceled
			}
			keys := make([]string, 0, len(batch))
			for _, it := range batch {
				keys = append(keys, it.PathKey)
			}
			var total, existing int64
			if er = tx.Model(&Item{}).Where("job_id = ?", j.ID).Count(&total).Error; er != nil {
				return er
			}
			if er = tx.Model(&Item{}).Where("job_id = ? AND path_key IN ?", j.ID, keys).Count(&existing).Error; er != nil {
				return er
			}
			if total+int64(len(batch))-existing > m.cfg.MaxItems {
				return ErrLimit
			}
			for _, it := range batch {
				res := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "job_id"}, {Name: "path_key"}}, DoNothing: true}).Create(&it)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					continue
				}
				if it.Kind == "file" {
					for _, p := range strings.Split(j.Profiles, ",") {
						state := "queued"
						if it.State == "skipped" {
							state = "skipped"
						}
						if er = tx.Create(&DerivativeItemProfile{ItemID: it.ID, Profile: p, State: state, Error: it.Error}).Error; er != nil {
							return er
						}
					}
				}
			}
			return nil
		})
		m.gate.Unlock()
		if e != nil {
			return e
		}
	}
	// The final checkpoint, revision observations, and absence reconciliation are
	// committed together only after every batch was persisted successfully.
	m.gate.Lock()
	defer m.gate.Unlock()
	return m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current Job
		if er := tx.First(&current, j.ID).Error; er != nil {
			return er
		}
		if current.State == "cancelled" {
			return context.Canceled
		}
		if er := observeSources(tx, sources); er != nil {
			return er
		}
		if er := revokeMissing(tx, j, item.Path, present); er != nil {
			return er
		}
		return tx.Model(&Item{}).Where("id = ? AND state = ?", item.ID, "active").Updates(map[string]any{"state": "succeeded", "stage": "scanned", "error": "", "lease_until": time.Time{}}).Error
	})
}

func (m *Manager) prepareProfiles(j Job, item *Item, src DerivativeSource) ([]DerivativeItemProfile, error) {
	var needed []DerivativeItemProfile
	e := m.db.Transaction(func(tx *gorm.DB) error {
		if er := observeSources(tx, []DerivativeSource{src}); er != nil {
			return er
		}
		changed := item.Revision != src.Revision
		if changed {
			if er := tx.Model(&DerivativeItemProfile{}).Where("item_id = ?", item.ID).Updates(map[string]any{"state": "queued", "artifact_id": "", "attempts": 0, "error": "", "bytes": 0}).Error; er != nil {
				return er
			}
		}
		if er := tx.Model(&Item{}).Where("id = ?", item.ID).Update("revision", src.Revision).Error; er != nil {
			return er
		}
		item.Revision = src.Revision
		var profiles []DerivativeItemProfile
		if er := tx.Where("item_id = ?", item.ID).Order("id").Find(&profiles).Error; er != nil {
			return er
		}
		var arts []Artifact
		if er := tx.Where("source_key = ? AND revision = ? AND version = ? AND state = ?", src.Key, src.Revision, ProfileVersion, "ready").Find(&arts).Error; er != nil {
			return er
		}
		ready := map[string]Artifact{}
		for _, a := range arts {
			if m.artifactFileValid(a, false) {
				ready[a.Profile] = a
			}
		}
		for _, p := range profiles {
			if p.State == "skipped" {
				continue
			}
			if a, ok := ready[p.Profile]; ok && (!j.Force || ((p.State == "ready" || p.State == "existing") && p.ArtifactID == a.ID)) {
				state := p.State
				if state != "ready" {
					state = "existing"
				}
				if er := tx.Model(&DerivativeItemProfile{}).Where("id = ?", p.ID).Updates(map[string]any{"state": state, "artifact_id": a.ID, "bytes": a.Bytes, "error": ""}).Error; er != nil {
					return er
				}
				continue
			}
			if p.State == "failed" && p.Attempts >= 3 {
				continue
			}
			needed = append(needed, p)
		}
		return nil
	})
	return needed, e
}

func (m *Manager) completeFile(item Item) error {
	var profiles []DerivativeItemProfile
	if e := m.db.Where("item_id = ?", item.ID).Find(&profiles).Error; e != nil {
		return e
	}
	ready, existing, failed, skipped, pending := 0, 0, 0, 0, 0
	message := ""
	for _, p := range profiles {
		switch p.State {
		case "ready":
			ready++
		case "existing":
			ready++
			existing++
		case "failed":
			failed++
			message = p.Error
		case "skipped":
			skipped++
			if message == "" {
				message = p.Error
			}
		default:
			pending++
		}
	}
	state := "succeeded"
	if pending > 0 {
		state = "retry_wait"
	} else if ready == len(profiles) && existing == ready {
		state = "existing"
	} else if ready > 0 && (failed > 0 || skipped > 0) {
		state = "partial_failed"
	} else if failed > 0 {
		state = "failed"
	} else if skipped > 0 {
		state = "skipped"
	}
	return m.db.Model(&Item{}).Where("id = ? AND state <> ?", item.ID, "cancelled").Updates(map[string]any{"state": state, "stage": "finished", "error": message, "lease_until": time.Time{}}).Error
}
