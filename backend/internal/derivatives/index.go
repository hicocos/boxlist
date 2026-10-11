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
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func digest(values ...string) string {
	h := sha256.New()
	for _, s := range values {
		fmt.Fprintf(h, "%d:", len(s))
		h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Binding includes identity-bearing source settings. Cloudreve's expiring JWTs
// are represented by their stable subject, not their rotating token bytes.
// The subject only identifies a configured source; it never grants access.
func Binding(s *model.Storage) string {
	if s == nil {
		return ""
	}
	addition := s.Addition
	var raw any
	if json.Unmarshal([]byte(addition), &raw) == nil {
		if fields, ok := raw.(map[string]any); ok && s.Driver == "Cloudreve V4" {
			stableCloudreveCredentials(fields)
		}
		if b, e := json.Marshal(raw); e == nil {
			addition = string(b)
		}
	}
	return digest(fmt.Sprint(s.ID), s.Driver, s.MountPath, addition, s.Remark)
}
func Revision(o model.Obj) string {
	if o == nil {
		return ""
	}
	return digest(fmt.Sprint(o.GetSize()), o.ModTime().UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), o.GetHash().String())
}

func unsupportedStorage(s *model.Storage) bool {
	name := strings.ToLower(s.Driver)
	return s.Disabled || strings.HasPrefix(s.Remark, "ref:/") || name == "alias" || name == "crypt" || strings.Contains(name, "reference") || strings.Contains(name, "union") || strings.Contains(name, "urltree") || strings.Contains(name, "url tree") || utils.GetActualMountPath(s.MountPath) != s.MountPath
}
func storageForPath(rows []model.Storage, p string) (*model.Storage, error) {
	var best *model.Storage
	ambiguous := false
	for i := range rows {
		s := &rows[i]
		mount := utils.GetActualMountPath(s.MountPath)
		if !inside(mount, p) {
			continue
		}
		if best == nil || len(mount) > len(utils.GetActualMountPath(best.MountPath)) {
			best = s
			ambiguous = false
		} else if len(mount) == len(utils.GetActualMountPath(best.MountPath)) {
			ambiguous = true
		}
	}
	if best == nil || ambiguous || unsupportedStorage(best) {
		return nil, ErrUnsupportedStorage
	}
	return best, nil
}
func (m *Manager) storages(ctx context.Context) ([]model.Storage, error) {
	var rows []model.Storage
	e := m.db.WithContext(ctx).Find(&rows).Error
	return rows, e
}
func newSource(s *model.Storage, p, rev string) DerivativeSource {
	return DerivativeSource{Key: digest(Binding(s), p), PathKey: digest(p), ParentKey: digest(path.Dir(p)), StorageID: s.ID, Binding: Binding(s), Path: p, Revision: rev, ObservedAt: time.Now()}
}

func sourceFromObject(s *model.Storage, p string, obj model.Obj) DerivativeSource {
	source := newSource(s, p, Revision(obj))
	source.ObservedAt = model.ObjObservedAt(obj)
	return source
}

// observeSources is transactional and uses bounded IN queries. Changing source
// metadata revokes previously issued artifact IDs, including direct URL reads.
func observeSources(tx *gorm.DB, sources []DerivativeSource) error {
	for start := 0; start < len(sources); start += 100 {
		end := start + 100
		if end > len(sources) {
			end = len(sources)
		}
		batch := sources[start:end]
		sourceKeys := make([]string, 0, len(batch))
		for _, s := range batch {
			sourceKeys = append(sourceKeys, s.Key)
		}
		var previous []DerivativeSource
		if e := tx.Where("source_key IN ?", sourceKeys).Find(&previous).Error; e != nil {
			return e
		}
		bySource := map[string]DerivativeSource{}
		for _, s := range previous {
			bySource[s.Key] = s
		}
		ordered := make([]DerivativeSource, 0, len(batch))
		for _, s := range batch {
			old, ok := bySource[s.Key]
			if !ok || old.ObservedAt.IsZero() || (!s.ObservedAt.IsZero() && !s.ObservedAt.Before(old.ObservedAt)) {
				ordered = append(ordered, s)
			}
		}
		batch = ordered
		if len(batch) == 0 {
			continue
		}
		keys := make([]string, 0, len(batch))
		byKey := map[string]DerivativeSource{}
		for _, s := range batch {
			keys = append(keys, s.PathKey)
			byKey[s.PathKey] = s
		}
		var arts []Artifact
		if e := tx.Where("path_key IN ? AND state = ?", keys, "ready").Find(&arts).Error; e != nil {
			return e
		}
		stale := []string{}
		for _, a := range arts {
			s := byKey[a.PathKey]
			if a.Binding != s.Binding || a.Revision != s.Revision || a.Version != ProfileVersion {
				stale = append(stale, a.ID)
			}
		}
		if len(stale) > 0 {
			if e := tx.Model(&Artifact{}).Where("id IN ?", stale).Updates(map[string]any{"state": "stale", "error": "source_changed"}).Error; e != nil {
				return e
			}
		}
		if e := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "source_key"}}, DoUpdates: clause.AssignmentColumns([]string{"revision", "deleted", "observed_at", "updated_at"})}).Create(&batch).Error; e != nil {
			return e
		}
	}
	return nil
}

func (m *Manager) Lookup(ctx context.Context, parent string, objs []model.Obj) (map[string]map[string]Artifact, error) {
	result := map[string]map[string]Artifact{}
	if !m.cfg.Enabled {
		return result, nil
	}
	parent, e := canonicalPath(parent)
	if e != nil {
		return result, e
	}
	rows, e := m.storages(ctx)
	if e != nil {
		return result, e
	}
	paths := []string{}
	sources := []DerivativeSource{}
	seen := map[string]bool{}
	for _, o := range objs {
		if o == nil || o.IsDir() || !validName(o.GetName()) {
			continue
		}
		p := path.Join(parent, o.GetName())
		if seen[p] {
			continue
		}
		seen[p] = true
		s, e := storageForPath(rows, p)
		if e != nil {
			continue
		}
		paths = append(paths, p)
		sources = append(sources, sourceFromObject(s, p, o))
	}
	// Browsing must not write a source row for every object on every page.
	// Only invalidate previously tracked sources whose version actually changed;
	// creation belongs to explicit scans/generation. This also avoids SQLite
	// reader-to-writer upgrade contention while the worker persists progress.
	changed := []DerivativeSource{}
	for start := 0; start < len(sources); start += 100 {
		end := min(start+100, len(sources))
		keys := make([]string, 0, end-start)
		for _, s := range sources[start:end] {
			keys = append(keys, s.PathKey)
		}
		var known []DerivativeSource
		if e = m.db.WithContext(ctx).Where("path_key IN ?", keys).Find(&known).Error; e != nil {
			return result, e
		}
		byPath := map[string][]DerivativeSource{}
		for _, s := range known {
			byPath[s.PathKey] = append(byPath[s.PathKey], s)
		}
		for _, s := range sources[start:end] {
			for _, old := range byPath[s.PathKey] {
				if !s.ObservedAt.IsZero() && (old.ObservedAt.IsZero() || !s.ObservedAt.Before(old.ObservedAt)) && (old.Binding != s.Binding || old.Revision != s.Revision || old.Deleted) {
					changed = append(changed, s)
					break
				}
			}
		}
	}
	if len(changed) > 0 {
		m.gate.Lock()
		e = m.observeListed(ctx, changed)
		m.gate.Unlock()
		if e != nil {
			return result, e
		}
	}
	found, e := m.lookupPaths(ctx, paths, rows)
	if e != nil {
		return result, e
	}
	for p, a := range found {
		result[path.Base(p)] = a
	}
	return result, nil
}

// LookupPaths uses only local SQL and local file metadata. It intentionally
// returns the last confirmed revision rather than probing the remote driver.
func (m *Manager) LookupPaths(ctx context.Context, paths []string) (map[string]map[string]Artifact, error) {
	if !m.cfg.Enabled {
		return map[string]map[string]Artifact{}, nil
	}
	rows, e := m.storages(ctx)
	if e != nil {
		return nil, e
	}
	return m.lookupPaths(ctx, paths, rows)
}
func (m *Manager) lookupPaths(ctx context.Context, paths []string, storages []model.Storage) (map[string]map[string]Artifact, error) {
	result := map[string]map[string]Artifact{}
	wanted := map[string]string{}
	keys := []string{}
	for _, p := range paths {
		p, e := canonicalPath(p)
		if e != nil {
			return result, e
		}
		k := digest(p)
		if _, ok := wanted[k]; !ok {
			wanted[k] = p
			keys = append(keys, k)
		}
	}
	for start := 0; start < len(keys); start += 200 {
		end := start + 200
		if end > len(keys) {
			end = len(keys)
		}
		batch := keys[start:end]
		var sources []DerivativeSource
		if e := m.db.WithContext(ctx).Where("path_key IN ? AND deleted = ?", batch, false).Find(&sources).Error; e != nil {
			return result, e
		}
		latest := map[string]DerivativeSource{}
		for _, s := range sources {
			latest[s.Key] = s
		}
		var arts []Artifact
		if e := m.db.WithContext(ctx).Where("path_key IN ? AND state = ? AND version = ?", batch, "ready", ProfileVersion).Find(&arts).Error; e != nil {
			return result, e
		}
		bindings := map[uint]string{}
		for _, a := range arts {
			if wanted[a.PathKey] != a.Path {
				continue
			}
			s, e := storageForPath(storages, a.Path)
			if e != nil || s.ID != a.StorageID {
				continue
			}
			b, ok := bindings[s.ID]
			if !ok {
				b = Binding(s)
				bindings[s.ID] = b
			}
			if b != a.Binding {
				continue
			}
			src, ok := latest[a.SourceKey]
			if !ok || src.Revision != a.Revision {
				continue
			}
			if !m.artifactFileValid(a, false) {
				continue
			}
			if result[a.Path] == nil {
				result[a.Path] = map[string]Artifact{}
			}
			result[a.Path][a.Profile] = a
		}
	}
	return result, nil
}

func (m *Manager) Artifact(id, profile string) (Artifact, error) {
	var a Artifact
	if !m.cfg.Enabled {
		return a, ErrDisabled
	}
	if !validArtifactID(id) || (profile != "thumb" && profile != "reader") {
		return a, gorm.ErrRecordNotFound
	}
	if e := m.db.Where("id = ? AND profile = ? AND state = ? AND version = ?", id, profile, "ready", ProfileVersion).First(&a).Error; e != nil {
		return a, e
	}
	var source DerivativeSource
	if e := m.db.Where("source_key = ? AND revision = ? AND deleted = ?", a.SourceKey, a.Revision, false).First(&source).Error; e != nil {
		return Artifact{}, gorm.ErrRecordNotFound
	}
	rows, e := m.storages(context.Background())
	if e != nil {
		return Artifact{}, e
	}
	s, e := storageForPath(rows, a.Path)
	if e != nil || s.ID != a.StorageID || Binding(s) != a.Binding {
		return Artifact{}, gorm.ErrRecordNotFound
	}
	if !m.artifactFileValid(a, false) {
		return Artifact{}, gorm.ErrRecordNotFound
	}
	return a, nil
}
func validArtifactID(id string) bool {
	if len(id) != 64 {
		return false
	}
	b, e := hex.DecodeString(id)
	return e == nil && len(b) == 32 && strings.ToLower(id) == id
}

// Only a successfully fetched, fully persisted directory may confirm absence.
// Descendant sources of a removed child directory are revoked as well; mounted
// children are never part of this binding and are not touched.
func revokeMissing(tx *gorm.DB, j Job, dir string, present map[string]bool) error {
	var sources []DerivativeSource
	if e := tx.Where("binding = ? AND deleted = ?", j.Binding, false).Find(&sources).Error; e != nil {
		return e
	}
	missing := []string{}
	for _, s := range sources {
		if !inside(dir, s.Path) || s.Path == dir {
			continue
		}
		rel := strings.TrimPrefix(s.Path, strings.TrimSuffix(dir, "/")+"/")
		name := strings.SplitN(rel, "/", 2)[0]
		if !present[name] {
			missing = append(missing, s.Key)
		}
	}
	for start := 0; start < len(missing); start += 100 {
		end := start + 100
		if end > len(missing) {
			end = len(missing)
		}
		keys := missing[start:end]
		if e := tx.Model(&DerivativeSource{}).Where("source_key IN ?", keys).Update("deleted", true).Error; e != nil {
			return e
		}
		if e := tx.Model(&Artifact{}).Where("source_key IN ? AND state = ?", keys, "ready").Updates(map[string]any{"state": "deleted", "error": "source_deleted"}).Error; e != nil {
			return e
		}
	}
	return nil
}

func (m *Manager) publish(j Job, item Item, src DerivativeSource, enc Encoded, tmp string) (Artifact, error) {
	return m.publishContext(m.ctx, j, item, src, enc, tmp)
}
func (m *Manager) publishContext(ctx context.Context, j Job, item Item, src DerivativeSource, enc Encoded, tmp string) (Artifact, error) {
	a := Artifact{ID: digest(src.Key, src.Revision, enc.Profile, fmt.Sprint(ProfileVersion), enc.SHA256), SourceKey: src.Key, PathKey: src.PathKey, ParentKey: src.ParentKey, StorageID: j.StorageID, Binding: j.Binding, Path: item.Path, Revision: src.Revision, Profile: enc.Profile, Version: ProfileVersion, State: "ready", Width: enc.Width, Height: enc.Height, Bytes: enc.Bytes, SHA256: enc.SHA256, Manual: j.RuleID == 0}
	a.File = path.Join("artifacts", a.ID[:2], a.ID+".webp")
	{
		target, err := m.s3WriteTarget()
		if err != nil {
			return a, err
		}
		minFree := m.cfg.MinFreeBytes
		if target != nil {
			minFree = 0
		}
		// The encoded file is already on disk; publishing needs no additional
		// work reservation, but a local destination still retains its floor.
		if err = m.diskAvailableForPolicy(0, minFree); err != nil {
			return a, err
		}
		if target != nil {
			a.OutputTargetID = target.ID
			a.ID = digest(a.ID, fmt.Sprint(target.ID))
			a.File = ""
			a.ObjectKey = target.Prefix + "artifacts/" + a.ID[:2] + "/" + a.ID + ".webp"
			m.remoteCleanupMu.RLock()
			defer m.remoteCleanupMu.RUnlock()
			if err = m.journalRemote(a); err != nil {
				return a, err
			}
			if err = m.s3Put(ctx, target, a.ObjectKey, tmp, a.Bytes, a.SHA256); err != nil {
				return a, err
			}
			if err = m.s3Head(ctx, target, a.ObjectKey, a.Bytes); err != nil {
				return a, err
			}
		}
	}
	m.gate.Lock()
	defer m.gate.Unlock()
	current, e := m.getJob(j.ID)
	if e != nil {
		return a, e
	}
	if err := m.checkPermission(ctx, j.Creator, item.Path); err != nil {
		return a, err
	}
	storages, err := m.storages(ctx)
	if err != nil {
		return a, err
	}
	bound, err := storageForPath(storages, item.Path)
	if err != nil || bound.ID != j.StorageID || Binding(bound) != j.Binding {
		return a, ErrBinding
	}
	if current.State == "cancelled" || m.ctx.Err() != nil || ctx.Err() != nil {
		return a, context.Canceled
	}
	e = m.db.Transaction(func(tx *gorm.DB) error {
		var check DerivativeSource
		if er := tx.First(&check, "source_key = ?", src.Key).Error; er != nil {
			return er
		}
		if check.Revision != src.Revision || check.Deleted {
			return ErrSourceChanged
		}
		if a.OutputTargetID == 0 {
			if er := m.commitFile(tmp, a.File); er != nil {
				return er
			}
		}
		// The immutable content-named file is published before the DB transaction.
		// A crash here produces a removable orphan, never a ready partial file.
		var previous Artifact
		er := tx.First(&previous, "id = ?", a.ID).Error
		if er == nil {
			a.Manual = a.Manual || previous.Manual
		}
		if er != nil && !errors.Is(er, gorm.ErrRecordNotFound) {
			return er
		}
		if er = tx.Model(&Artifact{}).Where("source_key = ? AND profile = ? AND id <> ? AND state = ?", src.Key, a.Profile, a.ID, "ready").Update("state", "obsolete").Error; er != nil {
			return er
		}
		if er = tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&a).Error; er != nil {
			return er
		}
		if er = tx.Model(&DerivativeItemProfile{}).Where("item_id = ? AND profile = ?", item.ID, a.Profile).Updates(map[string]any{"state": "ready", "artifact_id": a.ID, "bytes": a.Bytes, "error": ""}).Error; er != nil {
			return er
		}
		return tx.Model(&Item{}).Where("id = ?", item.ID).UpdateColumn("generated_bytes", gorm.Expr("generated_bytes + ?", a.Bytes)).Error
	})
	m.invalidateDisk()
	return a, e
}
