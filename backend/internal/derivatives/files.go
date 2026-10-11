package derivatives

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

func localInside(root, p string) bool {
	rel, e := filepath.Rel(root, p)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func noSymlinks(p string) error {
	p, e := filepath.Abs(p)
	if e != nil {
		return e
	}
	current := string(filepath.Separator)
	for _, part := range strings.Split(strings.TrimPrefix(p, current), string(filepath.Separator)) {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		st, e := os.Lstat(current)
		if e != nil {
			return e
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return errors.New("derivative_symlink_refused")
		}
	}
	return nil
}
func ensureDirectory(p string) error {
	// Check existing ancestors before MkdirAll follows them.
	ancestor := p
	for {
		_, e := os.Lstat(ancestor)
		if e == nil {
			if e = noSymlinks(ancestor); e != nil {
				return e
			}
			break
		}
		if !os.IsNotExist(e) {
			return e
		}
		next := filepath.Dir(ancestor)
		if next == ancestor {
			return ErrInvalidPath
		}
		ancestor = next
	}
	if e := os.MkdirAll(p, 0700); e != nil {
		return e
	}
	if e := noSymlinks(p); e != nil {
		return e
	}
	st, e := os.Stat(p)
	if e != nil {
		return e
	}
	if !st.IsDir() {
		return ErrInvalidPath
	}
	return nil
}

func (m *Manager) FilePath(a Artifact) (string, error) {
	if a.OutputTargetID != 0 || !validArtifactID(a.ID) || a.File == "" || strings.ContainsAny(a.File, "\\\x00") || filepath.IsAbs(a.File) {
		return "", ErrInvalidPath
	}
	clean := filepath.ToSlash(filepath.Clean(a.File))
	if clean != a.File || !strings.HasPrefix(clean, "artifacts/") {
		return "", ErrInvalidPath
	}
	expected := "artifacts/" + a.ID[:2] + "/" + a.ID + ".webp"
	if clean != expected {
		return "", ErrInvalidPath
	}
	p := filepath.Join(m.root, filepath.FromSlash(a.File))
	if !localInside(m.root, p) {
		return "", ErrInvalidPath
	}
	if e := noSymlinks(p); e != nil {
		return "", e
	}
	st, e := os.Lstat(p)
	if e != nil {
		return "", e
	}
	if !st.Mode().IsRegular() {
		return "", ErrInvalidPath
	}
	return p, nil
}
func (m *Manager) artifactFileValid(a Artifact, checksum bool) bool {
	if a.OutputTargetID != 0 {
		if !validArtifactID(a.ID) || (a.Profile != "thumb" && a.Profile != "reader") || a.File != "" || a.Bytes <= 0 || a.Bytes > s3MaxObjectBytes || !validArtifactID(a.SHA256) {
			return false
		}
		target, err := m.s3TargetForArtifact(a)
		objectID := a.ID
		if a.ObjectID != "" {
			objectID = a.ObjectID
		}
		key, keyErr := s3ArtifactKey(target, objectID)
		return err == nil && keyErr == nil && key == a.ObjectKey
	}
	p, e := m.FilePath(a)
	if e != nil {
		return false
	}
	st, e := os.Stat(p)
	if e != nil || st.Size() != a.Bytes || a.Bytes <= 0 {
		return false
	}
	if !checksum {
		return true
	}
	f, e := os.Open(p)
	if e != nil {
		return false
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return false
	}
	return hex.EncodeToString(h.Sum(nil)) == a.SHA256
}
func (m *Manager) commitFile(tmp, rel string) error {
	dest := filepath.Join(m.root, filepath.FromSlash(rel))
	if !localInside(filepath.Join(m.root, "artifacts"), dest) {
		return ErrInvalidPath
	}
	if !localInside(filepath.Join(m.root, "work"), tmp) {
		return ErrInvalidPath
	}
	if e := noSymlinks(tmp); e != nil {
		return e
	}
	if e := ensureDirectory(filepath.Dir(dest)); e != nil {
		return e
	}
	if st, e := os.Lstat(dest); e == nil {
		if !st.Mode().IsRegular() {
			return ErrInvalidPath
		}
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.OpenFile(tmp, os.O_RDWR, 0)
	if e != nil {
		return e
	}
	e = f.Sync()
	f.Close()
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, dest); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(dest))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}

func directoryBytes(root string) (int64, error) {
	var n int64
	e := filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("derivative_symlink_refused")
		}
		if d.Type().IsRegular() {
			st, e := d.Info()
			if e != nil {
				return e
			}
			n += st.Size()
		}
		return nil
	})
	return n, e
}
func (m *Manager) invalidateDisk() { m.diskMu.Lock(); m.diskAt = time.Time{}; m.diskMu.Unlock() }
func (m *Manager) usage() (int64, int64, error) {
	m.diskMu.Lock()
	defer m.diskMu.Unlock()
	if time.Since(m.diskAt) < 5*time.Second {
		return m.diskUsed, m.diskWork, m.diskErr
	}
	used, e := directoryBytes(filepath.Join(m.root, "artifacts"))
	work, we := directoryBytes(filepath.Join(m.root, "work"))
	if e == nil {
		e = we
	}
	m.diskUsed, m.diskWork, m.diskErr, m.diskAt = used, work, e, time.Now()
	return used, work, e
}

// S3 outputs only need transient work space; local outputs also retain the
// configured free-space floor. Resolve the same destination as publication.
func (m *Manager) storageDiskPolicy() (string, int64, error) {
	target, err := m.s3WriteTarget()
	if err != nil {
		return "", 0, err
	}
	if target != nil {
		return "s3", 0, nil
	}
	return "local", m.cfg.MinFreeBytes, nil
}

func (m *Manager) checkDiskAvailable(used, work, reserve int64, free uint64, minFree int64) error {
	if (m.cfg.BudgetBytes > 0 && used+work+reserve > m.cfg.BudgetBytes) || free < uint64(minFree+reserve) {
		return ErrDisk
	}
	return nil
}

func (m *Manager) diskAvailable(reserve int64) error {
	_, minFree, err := m.storageDiskPolicy()
	if err != nil {
		return err
	}
	return m.diskAvailableForPolicy(reserve, minFree)
}

func (m *Manager) diskAvailableForPolicy(reserve, minFree int64) error {
	used, work, e := m.usage()
	if e != nil {
		return ErrDisk
	}
	u, e := disk.Usage(m.root)
	if e != nil {
		return ErrDisk
	}
	return m.checkDiskAvailable(used, work, reserve, u.Free, minFree)
}
func (m *Manager) Health() any {
	used, work, e := m.usage()
	workers := m.WorkerConfiguration()
	storageTarget, minFree, se := m.storageDiskPolicy()
	u, de := disk.Usage(m.root)
	var free uint64
	if de == nil {
		free = u.Free
	}
	var count int64
	qe := m.db.Model(&Item{}).Where("state IN ?", pendingStates).Count(&count).Error
	m.encoderMu.RLock()
	encoder := "ready"
	if m.encoderError != "" {
		encoder = m.encoderError
	}
	m.encoderMu.RUnlock()
	status := "ok"
	if e != nil || de != nil || qe != nil || se != nil {
		status = "storage_unavailable"
	} else if m.checkDiskAvailable(used, work, workReserve, free, minFree) != nil {
		status = "disk_protection"
	}
	return map[string]any{"enabled": m.cfg.Enabled, "encoder": encoder, "encoder_available": encoder == "ready", "encoder_error": m.encoderErrorSafe(), "status": status, "used_bytes": used, "work_bytes": work, "budget_bytes": m.cfg.BudgetBytes, "free_bytes": free, "min_free_bytes": m.cfg.MinFreeBytes, "effective_min_free_bytes": minFree, "storage_target": storageTarget, "work_reserve_bytes": workReserve, "queue_length": count, "workers": workers.Workers, "active_workers": workers.ActiveWorkers, "min_workers": 1, "max_workers": 4, "download_limit_bytes": maxDownload, "source_pixel_limit": encoderMaxSourcePixels, "profile_version": ProfileVersion, "max_jobs": m.cfg.MaxJobs, "max_items_per_job": m.cfg.MaxItems, "max_rules": m.cfg.MaxRules, "download_bytes_per_second": m.cfg.DownloadBytesPerSecond, "scan_interval_ms": m.cfg.ScanIntervalMillis, "run_start_hour": m.cfg.RunStartHour, "run_end_hour": m.cfg.RunEndHour, "in_run_window": m.inRunWindow(time.Now())}
}
func (m *Manager) Storage() any { return m.Health() }

type CleanupResult struct {
	Files int64  `json:"files"`
	Bytes int64  `json:"bytes"`
	Mode  string `json:"mode"`
}

type CleanupTotals struct {
	// Files counts cleanup entries; temporary entries are work directories.
	Files int64 `json:"files"`
	Bytes int64 `json:"bytes"`
}

type CleanupAssessment struct {
	Temporary CleanupTotals `json:"temporary"`
	Stale     CleanupTotals `json:"stale"`
}

// AssessCleanup reads derivative storage and metadata only, never source files
// or the remote bucket. Any incomplete assessment fails instead of returning zero.
func (m *Manager) AssessCleanup(ctx context.Context) (CleanupAssessment, error) {
	var assessment CleanupAssessment
	if err := ctx.Err(); err != nil {
		return assessment, err
	}
	remote, err := m.processRemoteCleanup(ctx, false)
	if err != nil {
		return assessment, err
	}
	temporary, err := m.processLocalCleanup(ctx, "temporary", false)
	if err != nil {
		return assessment, err
	}
	stale, err := m.processLocalCleanup(ctx, "stale", false)
	if err != nil {
		return assessment, err
	}
	assessment.Temporary = CleanupTotals{Files: temporary.Files, Bytes: temporary.Bytes}
	assessment.Stale = CleanupTotals{Files: stale.Files + remote.Files, Bytes: stale.Bytes + remote.Bytes}
	return assessment, nil
}

func (m *Manager) Cleanup(scope string) (any, error) { return m.CleanupContext(m.ctx, scope) }
func (m *Manager) CleanupContext(ctx context.Context, scope string) (any, error) {
	remote := CleanupResult{}
	if scope == "stale" || scope == "obsolete" {
		var err error
		remote, err = m.cleanupRemote(ctx)
		if err != nil {
			return nil, err
		}
	}
	result, err := m.processLocalCleanup(ctx, scope, true)
	if err != nil {
		return nil, err
	}
	result.Files += remote.Files
	result.Bytes += remote.Bytes
	return result, nil
}

func (m *Manager) processLocalCleanup(ctx context.Context, scope string, remove bool) (CleanupResult, error) {
	if scope == "stale" {
		scope = "obsolete"
	}
	if scope == "temporary" {
		scope = "expired_work"
	}
	if scope != "expired_work" && scope != "obsolete" {
		return CleanupResult{}, fmt.Errorf("invalid cleanup mode")
	}
	m.gate.Lock()
	defer m.gate.Unlock()
	result := CleanupResult{Mode: scope}
	db := m.db.WithContext(ctx)
	cutoff := time.Now().Add(-24 * time.Hour)
	if scope == "expired_work" {
		entries, e := os.ReadDir(filepath.Join(m.root, "work"))
		if e != nil {
			return result, e
		}
		for _, entry := range entries {
			if e := ctx.Err(); e != nil {
				return result, e
			}
			if !strings.HasPrefix(entry.Name(), "item-") || !entry.IsDir() {
				continue
			}
			st, e := entry.Info()
			if e != nil {
				return result, e
			}
			if !st.ModTime().Before(cutoff) {
				continue
			}
			var id uint
			if _, e = fmt.Sscanf(entry.Name(), "item-%d-", &id); e != nil {
				continue
			}
			var active int64
			if e = db.Model(&Item{}).Where("id = ? AND state = ?", id, "active").Count(&active).Error; e != nil {
				return result, e
			}
			if active != 0 {
				continue
			}
			p := filepath.Join(m.root, "work", entry.Name())
			if e = noSymlinks(p); e != nil {
				return result, e
			}
			n, e := directoryBytes(p)
			if e != nil {
				return result, e
			}
			if remove {
				if e = os.RemoveAll(p); e != nil {
					return result, e
				}
			}
			result.Files++
			result.Bytes += n
		}
	} else {
		var cursor string
		for {
			var rows []Artifact
			if e := db.Where("output_target_id = 0 AND state IN ? AND id > ?", localCleanupStates, cursor).Order("id").Limit(100).Find(&rows).Error; e != nil {
				return result, e
			}
			if len(rows) == 0 {
				break
			}
			for _, a := range rows {
				cursor = a.ID
				var eligible int64
				if e := db.Model(&Artifact{}).Where("id = ? AND output_target_id = 0 AND state IN ?", a.ID, localCleanupStates).Count(&eligible).Error; e != nil {
					return result, e
				}
				if eligible == 0 {
					continue
				}
				p, e := m.FilePath(a)
				if e == nil {
					st, er := os.Stat(p)
					if er != nil {
						return result, er
					}
					if remove {
						if er = os.Remove(p); er != nil {
							return result, er
						}
					}
					result.Files++
					result.Bytes += st.Size()
				} else if !os.IsNotExist(e) {
					return result, e
				}
				if remove {
					if e = db.Where("id = ? AND state IN ?", a.ID, localCleanupStates).Delete(&Artifact{}).Error; e != nil {
						return result, e
					}
				}
			}
		}
		// Orphans are files published before an interrupted DB commit. They must be
		// old and completely unreferenced, not merely associated with an offline source.
		e := filepath.WalkDir(filepath.Join(m.root, "artifacts"), func(p string, d fs.DirEntry, e error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if e != nil {
				return e
			}
			if d.Type()&os.ModeSymlink != 0 {
				return ErrInvalidPath
			}
			if !d.Type().IsRegular() {
				return nil
			}
			name := strings.TrimSuffix(d.Name(), ".webp")
			if !validArtifactID(name) {
				return nil
			}
			st, e := d.Info()
			if e != nil {
				return e
			}
			if !st.ModTime().Before(cutoff) {
				return nil
			}
			var n int64
			if e = db.Model(&Artifact{}).Where("id = ?", name).Count(&n).Error; e != nil {
				return e
			}
			if n != 0 {
				return nil
			}
			if remove {
				if e = os.Remove(p); e != nil {
					return e
				}
			}
			result.Files++
			result.Bytes += st.Size()
			return nil
		})
		if e != nil {
			return result, e
		}
	}
	if remove {
		m.invalidateDisk()
		m.signal()
	}
	return result, nil
}
