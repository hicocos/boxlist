package iconlibrary

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func contentRevision(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func validRevision(revision string) bool {
	if len(revision) != 64 {
		return false
	}
	for _, c := range revision {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func readPrivateBytes(path string, size int64) ([]byte, error) {
	if size < 1 || size > MaxInputBytes {
		return nil, fault(413)
	}
	f, err := openPrivate(path, unix.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() != size {
		return nil, errors.New("private file size mismatch")
	}
	data, err := io.ReadAll(io.LimitReader(f, size+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != size {
		return nil, errors.New("private file changed")
	}
	return data, nil
}

// Revision is derived from served bytes, not content_sha: the latter continues
// identifying the original upload request for safe idempotent retries.
func (s *store) fillRevision(asset *Asset) error {
	if revision := s.revisions[asset.ID]; revision != "" {
		asset.Revision = revision
		return nil
	}
	data, err := readPrivateBytes(s.path("previews", asset.ID, previewSuffix(asset.Name)), asset.Bytes)
	if err != nil {
		return err
	}
	asset.Revision = contentRevision(data)
	s.revisions[asset.ID] = asset.Revision
	return nil
}

func (s *store) transparencyInput(id, revision string) (Asset, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.recoverReplacement(); err != nil {
		return Asset{}, nil, fault(503)
	}
	r, err := scanRecord(s.db.QueryRow("SELECT "+columns+" FROM assets WHERE id=? AND deleted_at IS NULL", id))
	if err == sql.ErrNoRows {
		return Asset{}, nil, fault(404)
	}
	if err != nil {
		return Asset{}, nil, fault(503)
	}
	if r.Format != "gif" {
		return Asset{}, nil, fault(415)
	}
	data, err := readPrivateBytes(s.path("previews", id, ".gif"), r.Bytes)
	if err != nil {
		return Asset{}, nil, fault(503)
	}
	r.Revision = contentRevision(data)
	if r.Revision != revision {
		return Asset{}, nil, fault(409)
	}
	return r.Asset, data, nil
}

type replacementJournal struct {
	ID       string `json:"id"`
	Stage    string `json:"stage"`
	Revision string `json:"revision"`
	Bytes    int64  `json:"bytes"`
}

// The journal is the commit point. It contains only the new GIF: no old bytes or
// recovery-original copies survive success. With the store lock held, readers
// cannot observe the two renames or metadata update separately. A durable journal
// rolls forward before any subsequent access and at startup after interruption.
func (s *store) replaceGIF(id, revision string, data []byte, authorize func() error) (Asset, error) {
	stage, err := s.stage(data)
	if err != nil {
		return Asset{}, fault(503)
	}
	keepStage := false
	defer func() {
		if !keepStage {
			os.Remove(stage)
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err = s.recoverReplacement(); err != nil {
		return Asset{}, fault(503)
	}
	r, err := scanRecord(s.db.QueryRow("SELECT "+columns+" FROM assets WHERE id=? AND deleted_at IS NULL", id))
	if err == sql.ErrNoRows {
		return Asset{}, fault(404)
	}
	if err != nil {
		return Asset{}, fault(503)
	}
	if r.Format != "gif" {
		return Asset{}, fault(415)
	}
	if err = s.fillRevision(&r.Asset); err != nil {
		return Asset{}, fault(503)
	}
	if r.Revision != revision {
		return Asset{}, fault(409)
	}
	var total int64
	if err = s.db.QueryRow("SELECT COALESCE(SUM(bytes+original_bytes),0) FROM assets").Scan(&total); err != nil {
		return Asset{}, fault(503)
	}
	if total-r.Bytes-r.originalBytes+2*int64(len(data)) > MaxStoredBytes {
		return Asset{}, fault(507)
	}
	if err = authorize(); err != nil {
		return Asset{}, err
	}
	journal := replacementJournal{ID: id, Stage: filepath.Base(stage), Revision: contentRevision(data), Bytes: int64(len(data))}
	encoded, err := json.Marshal(journal)
	if err != nil {
		return Asset{}, fault(503)
	}
	journalStage, err := s.stage(encoded)
	if err != nil {
		return Asset{}, fault(503)
	}
	defer os.Remove(journalStage)
	if err = syncDir(filepath.Join(s.root, ".staging")); err != nil {
		return Asset{}, fault(503)
	}
	if err = os.Rename(journalStage, filepath.Join(s.root, ".replacement")); err != nil {
		return Asset{}, fault(503)
	}
	keepStage = true
	if err = syncDir(s.root); err != nil {
		return Asset{}, fault(503)
	}
	if err = s.recoverReplacement(); err != nil {
		return Asset{}, fault(503)
	}
	r.Bytes, r.Revision = int64(len(data)), journal.Revision
	return r.Asset, nil
}

func (s *store) recoverReplacement() error {
	path := filepath.Join(s.root, ".replacement")
	f, err := openPrivate(path, unix.O_RDONLY)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var journal replacementJournal
	err = json.NewDecoder(io.LimitReader(f, 1024)).Decode(&journal)
	f.Close()
	if err != nil {
		return err
	}
	if !validAsset(journal.ID) || !validRevision(journal.Revision) || !strings.HasSuffix(journal.Stage, ".tmp") || !requestPattern.MatchString(strings.TrimSuffix(journal.Stage, ".tmp")) {
		return errors.New("invalid replacement journal")
	}
	r, err := scanRecord(s.db.QueryRow("SELECT "+columns+" FROM assets WHERE id=?", journal.ID))
	if err != nil {
		return err
	}
	if !validRecord(r) || r.deleted.Valid || r.Format != "gif" {
		return errors.New("invalid replacement target")
	}
	stage := filepath.Join(s.root, ".staging", journal.Stage)
	data, err := readPrivateBytes(stage, journal.Bytes)
	if err != nil {
		return err
	}
	if contentRevision(data) != journal.Revision {
		return errors.New("invalid replacement revision")
	}
	// The worker fully validates before staging. Hash verification is sufficient
	// here and keeps GIF decompression outside the store's serialization lock.
	for _, target := range []struct{ dir, suffix string }{{"originals", ".original"}, {"previews", ".gif"}} {
		// Link the durable new bytes to a temporary name, then atomically replace.
		temporary := s.path(".staging", journal.ID, ".replace")
		if err = os.Remove(temporary); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err = os.Link(stage, temporary); err != nil {
			return err
		}
		if err = os.Rename(temporary, s.path(target.dir, journal.ID, target.suffix)); err != nil {
			return err
		}
		if err = syncDir(filepath.Join(s.root, target.dir)); err != nil {
			return err
		}
	}
	// content_sha deliberately remains the hash of the upload request, not the
	// rewritten original. Retrying that upload returns the edited asset unchanged.
	if _, err = s.db.Exec("UPDATE assets SET bytes=?,original_bytes=? WHERE id=?", journal.Bytes, journal.Bytes, journal.ID); err != nil {
		return err
	}
	s.revisions[journal.ID] = journal.Revision
	if err = os.Remove(path); err != nil {
		return err
	}
	if err = syncDir(s.root); err != nil {
		return err
	}
	if err = os.Remove(stage); err != nil {
		return err
	}
	return syncDir(filepath.Join(s.root, ".staging"))
}
