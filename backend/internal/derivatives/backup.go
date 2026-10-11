package derivatives

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// The JSON envelope contains metadata and portable credentials, never image bytes.
// The admin backup UI encrypts this value; it must never be logged or cached.
const derivativeBackupVersion = 2

var (
	ErrBackupInvalid   = errors.New("derivative_backup_invalid")
	ErrBackupIntegrity = errors.New("derivative_backup_integrity_failed")
	ErrBackupFailed    = errors.New("derivative_backup_failed")
	ErrBackupConflict  = errors.New("derivative_backup_conflict")
	ErrBackupStorage   = errors.New("derivative_backup_storage_unavailable")
)

type S3PreviewBackup struct {
	Version      int                     `json:"version"`
	Enabled      bool                    `json:"enabled"`
	ActiveTarget uint                    `json:"active_target"`
	Targets      []S3PreviewBackupTarget `json:"targets"`
	Previews     []S3PreviewBackupEntry  `json:"previews"`
	SHA256       string                  `json:"sha256"`
}

type S3PreviewBackupTarget struct {
	ID     uint            `json:"id"`
	Config S3ConfigRequest `json:"config"`
}

type S3PreviewBackupEntry struct {
	Artifact  Artifact         `json:"artifact"`
	TargetID  uint             `json:"target_id"`
	ObjectKey string           `json:"object_key"`
	MountPath string           `json:"mount_path"`
	Source    DerivativeSource `json:"source"`
}

func backupChecksum(b S3PreviewBackup) string {
	b.SHA256 = ""
	h := sha256.New()
	if err := json.NewEncoder(h).Encode(b); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// BackupS3Previews exports only addressable, ready S3 previews, all retained S3
// targets, and the source observations required for immediate lookup. Local
// files, jobs, rules, upload journals and the installation's s3.key are excluded.
func (m *Manager) BackupS3Previews(ctx context.Context) (S3PreviewBackup, error) {
	m.remoteCleanupMu.Lock()
	defer m.remoteCleanupMu.Unlock()
	var b S3PreviewBackup
	var targets []DerivativeS3Target
	var artifacts []Artifact
	var sources []DerivativeSource
	var storages []model.Storage
	var setting DerivativeS3Setting
	m.gate.Lock()
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Order("id").Find(&targets).Error; e != nil {
			return e
		}
		if e := tx.Where("output_target_id > 0 AND state = ?", "ready").Order("id").Find(&artifacts).Error; e != nil {
			return e
		}
		if e := tx.Order("source_key").Find(&sources).Error; e != nil {
			return e
		}
		if e := tx.Find(&storages).Error; e != nil {
			return e
		}
		e := tx.First(&setting, 1).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			return nil
		}
		return e
	})
	m.gate.Unlock()
	if err != nil {
		return b, ErrBackupFailed
	}
	b.Version, b.Enabled, b.ActiveTarget = derivativeBackupVersion, setting.Enabled, setting.TargetID
	b.Targets, b.Previews = []S3PreviewBackupTarget{}, []S3PreviewBackupEntry{}
	byTarget := map[uint]*DerivativeS3Target{}
	for i := range targets {
		t := &targets[i]
		access, secret, e := m.s3Credentials(t)
		if e != nil {
			return S3PreviewBackup{}, e
		}
		byTarget[t.ID] = t
		b.Targets = append(b.Targets, S3PreviewBackupTarget{ID: t.ID, Config: S3ConfigRequest{Enabled: true, Endpoint: t.Endpoint, Bucket: t.Bucket, Region: t.Region, Prefix: t.Prefix, PathStyle: t.PathStyle, CDNURL: t.CDNURL, PublicCDN: t.PublicCDN, AccessKey: access, SecretKey: secret}})
	}
	bySource := map[string]DerivativeSource{}
	for _, s := range sources {
		bySource[s.Key] = s
	}
	for _, a := range artifacts {
		s, e := storageForPath(storages, a.Path)
		src, ok := bySource[a.SourceKey]
		if e != nil || !ok || src.Deleted || src.Revision != a.Revision || s.ID != a.StorageID || Binding(s) != a.Binding || src.Key != digest(a.Binding, a.Path) || src.Path != a.Path || src.Binding != a.Binding || src.StorageID != a.StorageID {
			return S3PreviewBackup{}, ErrBackupStorage
		}
		t := byTarget[a.OutputTargetID]
		objectID := a.ID
		if a.ObjectID != "" {
			objectID = a.ObjectID
		}
		key, e := s3ArtifactKey(t, objectID)
		if e != nil || key != a.ObjectKey || !m.artifactFileValid(a, false) {
			return S3PreviewBackup{}, ErrBackupInvalid
		}
		// Export the original publication identity so the retained key is verifiable.
		a.ID, a.ObjectID, a.Error, a.AccessedAt = objectID, "", "", nil
		b.Previews = append(b.Previews, S3PreviewBackupEntry{Artifact: a, TargetID: a.OutputTargetID, ObjectKey: a.ObjectKey, MountPath: s.MountPath, Source: src})
	}
	b.SHA256 = backupChecksum(b)
	if _, e := validatePreviewBackup(b); e != nil {
		return S3PreviewBackup{}, e
	}
	return b, nil
}

func validatePreviewBackup(b S3PreviewBackup) (map[uint]*DerivativeS3Target, error) {
	if b.Version != derivativeBackupVersion || !s3ValidDigest(b.SHA256) || b.SHA256 != backupChecksum(b) {
		return nil, ErrBackupIntegrity
	}
	targets := map[uint]*DerivativeS3Target{}
	identities := map[string]bool{}
	for _, entry := range b.Targets {
		r := entry.Config
		t, e := normalizeS3Target(r)
		if e != nil || entry.ID == 0 || targets[entry.ID] != nil || len(r.AccessKey) > 256 || len(r.SecretKey) > 4096 || strings.TrimSpace(r.AccessKey) == "" || strings.TrimSpace(r.SecretKey) == "" || strings.ContainsAny(r.AccessKey+r.SecretKey, "\x00\r\n") {
			return nil, ErrBackupInvalid
		}
		t.IdentityHash = s3Identity(t)
		if identities[t.IdentityHash] {
			return nil, ErrBackupInvalid
		}
		identities[t.IdentityHash] = true
		t.accessKey, t.secretKey = r.AccessKey, r.SecretKey
		targets[entry.ID] = t
	}
	if (b.Enabled && b.ActiveTarget == 0) || (b.ActiveTarget != 0 && targets[b.ActiveTarget] == nil) {
		return nil, ErrBackupInvalid
	}
	seen := map[string]bool{}
	profiles := map[string]bool{}
	sources := map[string]DerivativeSource{}
	for _, entry := range b.Previews {
		a, src := entry.Artifact, entry.Source
		p, e := canonicalPath(a.Path)
		mount, me := canonicalPath(entry.MountPath)
		key, ke := s3ArtifactKey(targets[entry.TargetID], a.ID)
		if e != nil || me != nil || p != a.Path || mount != entry.MountPath || !inside(mount, p) || p == mount || ke != nil || key != entry.ObjectKey || seen[a.ID] || a.State != "ready" || a.Version != ProfileVersion || (a.Profile != "thumb" && a.Profile != "reader") || a.Width <= 0 || a.Height <= 0 || a.Bytes <= 0 || a.Bytes > s3MaxObjectBytes || !s3ValidDigest(a.SHA256) || !s3ValidDigest(a.Revision) || !s3ValidDigest(a.Binding) || src.Deleted || src.Path != p || src.StorageID != a.StorageID || src.Binding != a.Binding || src.Revision != a.Revision || src.Key != digest(a.Binding, p) || src.PathKey != digest(p) || src.ParentKey != digest(path.Dir(p)) {
			return nil, ErrBackupInvalid
		}
		profileKey := digest(p, a.Profile)
		if profiles[profileKey] {
			return nil, ErrBackupInvalid
		}
		profiles[profileKey] = true
		if old, ok := sources[src.Key]; ok && old != src {
			return nil, ErrBackupInvalid
		}
		sources[src.Key] = src
		seen[a.ID] = true
	}
	return targets, nil
}

// restoreSnapshot is checked again before committing so concurrent configuration,
// source observation or publication cannot turn a merge into a lost update.
type restoreSnapshot struct {
	Targets   []DerivativeS3Target
	Settings  []DerivativeS3Setting
	Sources   []DerivativeSource
	Artifacts []Artifact
	Storages  []model.Storage
}

func loadRestoreSnapshot(tx *gorm.DB) (restoreSnapshot, error) {
	var s restoreSnapshot
	if e := tx.Order("id").Find(&s.Targets).Error; e != nil {
		return s, e
	}
	if e := tx.Order("id").Find(&s.Settings).Error; e != nil {
		return s, e
	}
	if e := tx.Order("source_key").Find(&s.Sources).Error; e != nil {
		return s, e
	}
	if e := tx.Order("id").Find(&s.Artifacts).Error; e != nil {
		return s, e
	}
	if e := tx.Order("id").Find(&s.Storages).Error; e != nil {
		return s, e
	}
	return s, nil
}

// Private GORM fields are deliberately included in the concurrency fingerprint.
func restoreFingerprint(s restoreSnapshot) string {
	parts := []string{}
	for _, t := range s.Targets {
		parts = append(parts, fmt.Sprintf("%#v", t))
	}
	for _, t := range s.Settings {
		parts = append(parts, fmt.Sprintf("%#v", t))
	}
	for _, t := range s.Sources {
		parts = append(parts, fmt.Sprintf("%#v", t))
	}
	for _, t := range s.Artifacts {
		t.AccessedAt = nil
		parts = append(parts, fmt.Sprintf("%#v", t))
	}
	for _, t := range s.Storages {
		parts = append(parts, fmt.Sprintf("%#v", t))
	}
	return digest(parts...)
}

type restorePreview struct {
	artifact Artifact
	source   DerivativeSource
}

func (m *Manager) RestoreS3Previews(ctx context.Context, b S3PreviewBackup, override bool) error {
	portable, e := validatePreviewBackup(b)
	if e != nil {
		return e
	}
	m.remoteCleanupMu.Lock()
	defer m.remoteCleanupMu.Unlock()
	m.gate.Lock()
	snapshot, e := loadRestoreSnapshot(m.db.WithContext(ctx))
	m.gate.Unlock()
	if e != nil {
		return ErrBackupFailed
	}
	fingerprint := restoreFingerprint(snapshot)
	var maxID uint
	byIdentity := map[string]DerivativeS3Target{}
	for _, t := range snapshot.Targets {
		byIdentity[t.IdentityHash] = t
		if t.ID > maxID {
			maxID = t.ID
		}
	}
	mapped := map[uint]*DerivativeS3Target{}
	newTargets := []DerivativeS3Target{}
	for _, entry := range b.Targets {
		t := *portable[entry.ID]
		old, exists := byIdentity[t.IdentityHash]
		if exists && !override {
			mapped[entry.ID] = &old
			continue
		}
		if exists {
			t.ID = old.ID
			t.CreatedAt = old.CreatedAt
		} else {
			maxID++
			if maxID == 0 {
				return ErrBackupConflict
			}
			t.ID = maxID
		}
		key, err := m.s3EncryptionKey(true)
		if err != nil {
			return err
		}
		t.Credentials, err = sealS3Credentials(key, t.IdentityHash, t.accessKey, t.secretKey)
		if err != nil {
			return err
		}
		t.accessKey, t.secretKey = "", ""
		mapped[entry.ID] = &t
		newTargets = append(newTargets, t)
	}
	sources := map[string]DerivativeSource{}
	for _, s := range snapshot.Sources {
		sources[s.Key] = s
	}
	occupied := map[string]bool{}
	for _, a := range snapshot.Artifacts {
		if a.State == "ready" {
			occupied[digest(a.Path, a.Profile)] = true
		}
	}
	plans := []restorePreview{}
	for _, entry := range b.Previews {
		a := entry.Artifact
		s, err := storageForPath(snapshot.Storages, a.Path)
		if err != nil || s.MountPath != entry.MountPath {
			return ErrBackupStorage
		}
		src := newSource(s, a.Path, a.Revision)
		src.ObservedAt, src.UpdatedAt = entry.Source.ObservedAt, entry.Source.UpdatedAt
		old, exists := sources[src.Key]
		if !override && (occupied[digest(a.Path, a.Profile)] || (exists && (old.Deleted || old.Revision != src.Revision))) {
			continue
		}
		t := mapped[entry.TargetID]
		a.SourceKey, a.PathKey, a.ParentKey = src.Key, src.PathKey, src.ParentKey
		a.StorageID, a.Binding, a.OutputTargetID = s.ID, src.Binding, t.ID
		// Remap the local identity while retaining the validated original S3 key.
		if s.ID != entry.Artifact.StorageID || src.Binding != entry.Artifact.Binding || t.ID != entry.TargetID {
			base := digest(src.Key, a.Revision, a.Profile, fmt.Sprint(a.Version), a.SHA256)
			a.ID = digest(base, fmt.Sprint(t.ID))
		}
		a.File, a.Error, a.AccessedAt = "", "", nil
		a.ObjectKey, a.ObjectID = entry.ObjectKey, entry.Artifact.ID
		plans = append(plans, restorePreview{artifact: a, source: src})
	}
	m.gate.Lock()
	defer m.gate.Unlock()
	e = m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		current, err := loadRestoreSnapshot(tx)
		if err != nil {
			return ErrBackupFailed
		}
		if restoreFingerprint(current) != fingerprint {
			return ErrBackupConflict
		}
		for _, t := range newTargets {
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, UpdateAll: true}).Create(&t).Error; err != nil {
				return ErrBackupFailed
			}
		}
		seen := map[string]bool{}
		for _, plan := range plans {
			src, a := plan.source, plan.artifact
			if !seen[src.Key] {
				if override {
					if err := tx.Model(&Artifact{}).Where("path_key = ? AND state = ? AND (binding <> ? OR revision <> ?)", src.PathKey, "ready", src.Binding, src.Revision).Updates(map[string]any{"state": "stale", "error": "source_changed"}).Error; err != nil {
						return ErrBackupFailed
					}
					if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_key"}}, UpdateAll: true}).Create(&src).Error; err != nil {
						return ErrBackupFailed
					}
				} else {
					if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&src).Error; err != nil {
						return ErrBackupFailed
					}
				}
				seen[src.Key] = true
			}
			if override {
				if err := tx.Model(&Artifact{}).Where("path_key = ? AND profile = ? AND state = ? AND id <> ?", a.PathKey, a.Profile, "ready", a.ID).Updates(map[string]any{"state": "obsolete", "error": ""}).Error; err != nil {
					return ErrBackupFailed
				}
			}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, UpdateAll: true}).Create(&a).Error; err != nil {
				return ErrBackupFailed
			}
			journal := DerivativeRemoteObject{ID: digest(fmt.Sprint(a.OutputTargetID), a.ObjectKey), TargetID: a.OutputTargetID, ArtifactID: a.ID, ObjectID: a.ObjectID, ObjectKey: a.ObjectKey, Bytes: a.Bytes}
			if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "id"}}, DoUpdates: clause.AssignmentColumns([]string{"artifact_id", "object_id", "bytes", "updated_at"})}).Create(&journal).Error; err != nil {
				return ErrBackupFailed
			}
		}
		if override || len(snapshot.Settings) == 0 {
			var targetID uint
			if b.ActiveTarget != 0 {
				targetID = mapped[b.ActiveTarget].ID
			}
			setting := DerivativeS3Setting{ID: 1, Enabled: b.Enabled, TargetID: targetID, UpdatedAt: time.Now()}
			if err := tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&setting).Error; err != nil {
				return ErrBackupFailed
			}
		}
		return nil
	})
	if e != nil {
		return e
	}
	return nil
}
