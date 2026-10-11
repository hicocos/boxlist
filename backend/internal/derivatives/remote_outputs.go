package derivatives

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Journal before upload: exact keys remain reclaimable after cancellation or a
// failed metadata commit. Never list/delete arbitrary bucket objects.
type DerivativeRemoteObject struct {
	ID         string `gorm:"size:64;primaryKey"`
	TargetID   uint   `gorm:"index"`
	ArtifactID string `gorm:"size:64;index"`
	ObjectID   string `gorm:"size:64"`
	ObjectKey  string `gorm:"type:text"`
	Bytes      int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (m *Manager) remoteTarget(a Artifact) (*DerivativeS3Target, error) {
	if !validArtifactID(a.ID) || (a.Profile != "thumb" && a.Profile != "reader") || a.OutputTargetID == 0 || a.File != "" || a.Bytes <= 0 || a.Bytes > s3MaxObjectBytes || !s3ValidDigest(a.SHA256) {
		return nil, ErrInvalidPath
	}
	target, e := m.s3TargetForArtifact(a)
	if e != nil {
		return nil, e
	}
	objectID := a.ID
	if a.ObjectID != "" {
		objectID = a.ObjectID
	}
	key, err := s3ArtifactKey(target, objectID)
	if err != nil || a.ObjectKey != key {
		return nil, ErrInvalidPath
	}
	return target, nil
}

// Acquire before allocating a response: remote reads are bounded separately
// from the encoder and cancelled by the client, with no disk read-through cache.
func (m *Manager) OpenArtifact(ctx context.Context, a Artifact) (io.ReadSeekCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if a.OutputTargetID == 0 {
		p, e := m.FilePath(a)
		if e != nil {
			return nil, e
		}
		return openLocalArtifact(p, a.Bytes)
	}
	select {
	case m.remoteReads <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-m.remoteReads }
	target, e := m.remoteTarget(a)
	if e != nil {
		release()
		return nil, e
	}
	data, e := m.s3Get(ctx, target, a.ObjectKey, a.Bytes, a.SHA256)
	if e != nil {
		// Only confirmed missing/corrupt content is invalidated; outages never mean
		// a deleted source. No credentials or upstream bodies escape through errors.
		if ErrorCode(e) == "s3_object_missing" || ErrorCode(e) == "s3_checksum_mismatch" {
			_ = m.db.Model(&Artifact{}).Where("id = ? AND state = ? AND output_target_id = ?", a.ID, "ready", a.OutputTargetID).Updates(map[string]any{"state": "missing", "error": ErrorCode(e)}).Error
		}
		release()
		return nil, e
	}
	return &remoteArtifactReader{Reader: bytes.NewReader(data), release: release}, nil
}
func openLocalArtifact(path string, expected int64) (io.ReadSeekCloser, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() != expected {
		f.Close()
		if e != nil {
			return nil, e
		}
		return nil, ErrInvalidPath
	}
	return f, nil
}

type remoteArtifactReader struct {
	*bytes.Reader
	release func()
}

func (r *remoteArtifactReader) Close() error {
	if r.release != nil {
		r.release()
		r.release = nil
	}
	r.Reader = nil
	return nil
}

func (m *Manager) journalRemote(a Artifact) error {
	row := DerivativeRemoteObject{ID: digest(fmt.Sprint(a.OutputTargetID), a.ObjectKey), TargetID: a.OutputTargetID, ArtifactID: a.ID, ObjectID: a.ObjectID, ObjectKey: a.ObjectKey, Bytes: a.Bytes}
	return m.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

var localCleanupStates = []string{"stale", "obsolete", "deleted", "missing"}
var remoteCleanupStates = []string{"stale", "obsolete", "deleted", "missing", "cleaning"}

// Remote cleanup has its own serialization. Network calls must never hold the
// worker gate. Immutable keys are not reused by different output targets.
func (m *Manager) cleanupRemote(ctx context.Context) (CleanupResult, error) {
	return m.processRemoteCleanup(ctx, true)
}

func (m *Manager) processRemoteCleanup(ctx context.Context, remove bool) (CleanupResult, error) {
	m.remoteCleanupMu.Lock()
	defer m.remoteCleanupMu.Unlock()
	result := CleanupResult{Mode: "stale"}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	db := m.db.WithContext(ctx)
	var cursor string
	for {
		var rows []Artifact
		e := db.Where("output_target_id > 0 AND state IN ? AND id > ?", remoteCleanupStates, cursor).Order("id").Limit(100).Find(&rows).Error
		if e != nil {
			return result, e
		}
		if len(rows) == 0 {
			break
		}
		for _, a := range rows {
			cursor = a.ID
			// Recheck both eligibility and retained-key references under the gate.
			// Assessment virtually removes earlier eligible rows, so shared stale
			// keys count once, while any non-cleanable reference protects the object.
			m.gate.Lock()
			e := db.Where("id = ? AND output_target_id > 0 AND state IN ?", a.ID, remoteCleanupStates).First(&a).Error
			if errors.Is(e, gorm.ErrRecordNotFound) {
				m.gate.Unlock()
				continue
			}
			var references int64
			if e == nil {
				q := db.Model(&Artifact{}).Where("output_target_id = ? AND object_key = ? AND id <> ?", a.OutputTargetID, a.ObjectKey, a.ID)
				if !remove {
					q = q.Where("(state NOT IN ? OR id > ?)", remoteCleanupStates, a.ID)
				}
				e = q.Count(&references).Error
			}
			if e == nil && remove {
				if references > 0 {
					e = db.Where("id = ? AND state IN ?", a.ID, remoteCleanupStates).Delete(&Artifact{}).Error
				} else {
					e = db.Model(&Artifact{}).Where("id = ? AND state IN ?", a.ID, remoteCleanupStates).Update("state", "cleaning").Error
				}
			}
			m.gate.Unlock()
			if e != nil {
				return result, e
			}
			if references > 0 {
				continue
			}
			target, e := m.remoteTarget(a)
			if !remove {
				if e != nil {
					return result, e
				}
				result.Files++
				result.Bytes += a.Bytes
				continue
			}
			if e == nil {
				e = m.s3Delete(ctx, target, a.ObjectKey)
			}
			if e == nil {
				if er := m.s3Head(ctx, target, a.ObjectKey, a.Bytes); !errors.Is(er, ErrS3ObjectMissing) {
					e = ErrS3DeleteFailed
				}
			}
			if e != nil {
				_ = db.Model(&Artifact{}).Where("id = ? AND state = ?", a.ID, "cleaning").Update("state", a.State).Error
				return result, e
			}
			if e = db.Transaction(func(tx *gorm.DB) error {
				if e := tx.Where("id = ? AND state = ?", a.ID, "cleaning").Delete(&Artifact{}).Error; e != nil {
					return e
				}
				return tx.Where("target_id = ? AND object_key = ?", a.OutputTargetID, a.ObjectKey).Delete(&DerivativeRemoteObject{}).Error
			}); e != nil {
				return result, e
			}
			result.Files++
			result.Bytes += a.Bytes
		}
	}
	// Clean only our journaled, old, unreferenced uploads. Never infer bucket
	// ownership from a prefix or enumerate a provider bucket during admin reads.
	cutoff := time.Now().Add(-24 * time.Hour)
	cursor = ""
	for {
		var pending []DerivativeRemoteObject
		if e := db.Where("id > ? AND created_at < ? AND artifact_id NOT IN (?)", cursor, cutoff, db.Model(&Artifact{}).Select("id")).Order("id").Limit(100).Find(&pending).Error; e != nil {
			return result, e
		}
		if len(pending) == 0 {
			break
		}
		for _, entry := range pending {
			cursor = entry.ID
			var a Artifact
			var references int64
			if e := db.Model(&Artifact{}).Where("output_target_id = ? AND object_key = ?", entry.TargetID, entry.ObjectKey).Count(&references).Error; e != nil {
				return result, e
			}
			if references > 0 {
				continue
			}
			e := db.First(&a, "id = ?", entry.ArtifactID).Error
			if e == nil {
				continue
			}
			if !errors.Is(e, gorm.ErrRecordNotFound) {
				return result, e
			}
			// Safe immutable stub; remoteTarget validates metadata before deletion.
			a = Artifact{ID: entry.ArtifactID, ObjectID: entry.ObjectID, Profile: "thumb", OutputTargetID: entry.TargetID, ObjectKey: entry.ObjectKey, Bytes: entry.Bytes, SHA256: entry.ArtifactID}
			target, e := m.remoteTarget(a)
			if e != nil {
				return result, e
			}
			if !remove {
				result.Files++
				result.Bytes += entry.Bytes
				continue
			}
			if e = m.s3Delete(ctx, target, a.ObjectKey); e != nil {
				return result, e
			}
			if er := m.s3Head(ctx, target, a.ObjectKey, a.Bytes); !errors.Is(er, ErrS3ObjectMissing) {
				return result, ErrS3DeleteFailed
			}
			if e = db.Delete(&entry).Error; e != nil {
				return result, e
			}
			result.Files++
			result.Bytes += entry.Bytes
		}
	}
	return result, nil
}
