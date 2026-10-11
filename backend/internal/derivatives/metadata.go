package derivatives

import "time"

// Missing is a local index-only downgrade; it never resolves or fetches a source.
func (m *Manager) Missing(id string) {
	if !validArtifactID(id) {
		return
	}
	m.db.Model(&Artifact{}).Where("id = ? AND state = ?", id, "ready").Updates(map[string]any{"state": "missing", "error": "local_artifact_missing_or_corrupt"})
}

// Touch is throttled in SQL so requests across different HTTP workers cannot
// create a write storm or expose source links in an access log.
func (m *Manager) Touch(id string) {
	if !validArtifactID(id) {
		return
	}
	now := time.Now()
	m.db.Model(&Artifact{}).Where("id = ? AND state = ? AND (accessed_at IS NULL OR accessed_at < ?)", id, "ready", now.Add(-time.Hour)).UpdateColumn("accessed_at", now)
}
func (m *Manager) encoderErrorSafe() string {
	m.encoderMu.RLock()
	defer m.encoderMu.RUnlock()
	return m.encoderError
}
