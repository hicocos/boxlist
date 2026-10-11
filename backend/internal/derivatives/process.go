package derivatives

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/gorm"
)

// processFile owns one bounded temporary original for all requested profiles.
// Successful profiles are immutable and never removed when another profile fails.
func (m *Manager) processFile(ctx context.Context, j Job, item Item) error {
	b, actual, e := m.validateJob(ctx, j, item.Path)
	if e != nil {
		return e
	}
	obj, e := m.fresh(ctx, b, actual)
	if e != nil {
		return e
	}
	if obj.IsDir() {
		return ErrInvalidPath
	}
	src := sourceFromObject(b.Storage, item.Path, obj)
	m.gate.Lock()
	profiles, e := m.prepareProfiles(j, &item, src)
	m.gate.Unlock()
	if e != nil {
		return e
	}
	if len(profiles) == 0 {
		return m.completeFile(item)
	}
	if obj.GetSize() > maxDownload {
		return &EncodeError{Code: "skipped_limit", Err: errors.New("source exceeds download limit")}
	}
	if e = m.diskAvailable(workReserve); e != nil {
		return e
	}
	if e = noSymlinks(filepath.Join(m.root, "work")); e != nil {
		return e
	}
	work, e := os.MkdirTemp(filepath.Join(m.root, "work"), fmt.Sprintf("item-%d-", item.ID))
	if e != nil {
		return e
	}
	defer func() { os.RemoveAll(work); m.invalidateDisk() }()
	input := filepath.Join(work, "source")
	if e = m.db.Model(&Item{}).Where("id = ? AND state = ?", item.ID, "active").Update("stage", "downloading").Error; e != nil {
		return e
	}
	n, e := m.download(ctx, b, obj, input)
	if er := m.db.Model(&Item{}).Where("id = ?", item.ID).UpdateColumn("downloaded_bytes", gorm.Expr("downloaded_bytes + ?", n)).Error; er != nil {
		return er
	}
	if e != nil {
		return e
	}
	if _, e = InspectImage(input); e != nil {
		return e
	}
	var retryErr error
	for _, profile := range profiles {
		if e = ctx.Err(); e != nil {
			return e
		}
		if _, _, e = m.validateJob(ctx, j, item.Path); e != nil {
			return e
		}
		if e = m.diskAvailable(64 << 20); e != nil {
			return e
		}
		if e = m.db.Model(&Item{}).Where("id = ? AND state = ?", item.ID, "active").Update("stage", "encoding").Error; e != nil {
			return e
		}
		if e = m.db.Model(&DerivativeItemProfile{}).Where("id = ?", profile.ID).Updates(map[string]any{"state": "active", "attempts": gorm.Expr("attempts + 1")}).Error; e != nil {
			return e
		}
		profile.Attempts++
		output := filepath.Join(work, profile.Profile+".webp")
		enc, encodeErr := Encode(ctx, input, output, profile.Profile)
		if encodeErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if pauseError(encodeErr) {
				m.db.Model(&DerivativeItemProfile{}).Where("id = ?", profile.ID).UpdateColumn("attempts", gorm.Expr("CASE WHEN attempts > 0 THEN attempts - 1 ELSE 0 END"))
				return encodeErr
			}
			state := "failed"
			if skipError(encodeErr) {
				state = "skipped"
			} else if retryable(encodeErr) && profile.Attempts < 3 {
				state = "retry_wait"
				retryErr = encodeErr
			}
			if e = m.db.Model(&DerivativeItemProfile{}).Where("id = ?", profile.ID).Updates(map[string]any{"state": state, "error": ErrorCode(encodeErr)}).Error; e != nil {
				return e
			}
			continue
		}
		// InspectImage/Encode validate WebP themselves; independently verify the
		// reported output and source version before making it addressable.
		if enc.Profile != profile.Profile || enc.Bytes <= 0 || enc.Width <= 0 || enc.Height <= 0 || !validArtifactID(enc.SHA256) {
			return &EncodeError{Code: "encode_failed", Err: errors.New("invalid encoder output")}
		}
		if _, _, e = m.validateJob(ctx, j, item.Path); e != nil {
			return e
		}
		after, e := m.fresh(ctx, b, actual)
		if e != nil {
			return e
		}
		if Revision(after) != src.Revision {
			m.gate.Lock()
			er := m.db.Transaction(func(tx *gorm.DB) error {
				return observeSources(tx, []DerivativeSource{sourceFromObject(b.Storage, item.Path, after)})
			})
			m.gate.Unlock()
			if er != nil {
				return er
			}
			return ErrSourceChanged
		}
		if e = m.db.Model(&Item{}).Where("id = ? AND state = ?", item.ID, "active").Update("stage", "publishing").Error; e != nil {
			return e
		}
		if _, e = m.publishContext(ctx, j, item, src, enc, output); e != nil {
			return e
		}
	}
	if retryErr != nil {
		next := time.Now().Add(retryDelay(item.Attempts, retryErr))
		if e = m.db.Model(&Item{}).Where("id = ? AND state <> ?", item.ID, "cancelled").Updates(map[string]any{"state": "retry_wait", "stage": "encoding", "error": ErrorCode(retryErr), "next_attempt": next, "lease_until": time.Time{}}).Error; e != nil {
			return e
		}
		return nil
	}
	return m.completeFile(item)
}
