package derivatives

import (
	"context"
	"gorm.io/gorm"
	"strings"
	"time"
)

// A WAL reader upgraded to writer can see SQLITE_BUSY_SNAPSHOT while a worker
// records bytes/progress on another connection. Restart the entire transaction,
// bounded to short local waits. Never retry source/network work from a GET.
func (m *Manager) observeListed(ctx context.Context, sources []DerivativeSource) error {
	return m.transaction(ctx, func(tx *gorm.DB) error {
		current := make([]DerivativeSource, 0, len(sources))
		for start := 0; start < len(sources); start += 100 {
			end := min(start+100, len(sources))
			keys := []string{}
			for _, s := range sources[start:end] {
				keys = append(keys, s.Key)
			}
			var known []DerivativeSource
			if e := tx.Where("source_key IN ?", keys).Find(&known).Error; e != nil {
				return e
			}
			byKey := map[string]DerivativeSource{}
			for _, s := range known {
				byKey[s.Key] = s
			}
			for _, s := range sources[start:end] {
				old, ok := byKey[s.Key]
				if !ok || (!s.ObservedAt.IsZero() && !s.ObservedAt.Before(old.ObservedAt)) {
					current = append(current, s)
				}
			}
		}
		return observeSources(tx, current)
	})
}

func (m *Manager) transaction(ctx context.Context, operation func(*gorm.DB) error) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		err = m.db.WithContext(ctx).Transaction(operation)
		if err == nil {
			return nil
		}
		text := strings.ToLower(err.Error())
		if !strings.Contains(text, "locked") && !strings.Contains(text, "sqlite_busy") {
			return err
		}
		if e := waitDerivativePace(ctx, time.Duration(attempt+1)*10*time.Millisecond); e != nil {
			return e
		}
	}
	return err
}
