package derivatives

import (
	"context"
	"gorm.io/gorm"
	"path"
	"strings"
	"time"
)

// A successful in-app mutation is authoritative even if this directory has no
// auto rule. Revoke local grants immediately; enqueue only a cheap parent event.
func (m *Manager) Mutated(ctx context.Context, storageID uint, p string) error {
	if _, e := canonicalPath(p); e != nil {
		return e
	}
	defer m.Notify(path.Dir(p))
	// SQL LIKE metacharacters are escaped: filenames cannot enlarge revocation.
	escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(p)
	m.gate.Lock()
	defer m.gate.Unlock()
	return m.transaction(ctx, func(tx *gorm.DB) error {
		paths := "storage_id = ? AND (path = ? OR path LIKE ? ESCAPE '!')"
		if e := tx.Model(&Artifact{}).Where(paths, storageID, p, escaped+"/%").Where("state = ?", "ready").Updates(map[string]any{"state": "stale", "error": "source_changed"}).Error; e != nil {
			return e
		}
		return tx.Model(&DerivativeSource{}).Where(paths, storageID, p, escaped+"/%").Updates(map[string]any{"deleted": true, "observed_at": time.Now()}).Error
	})
}
