package iconlibrary

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "github.com/glebarez/go-sqlite"
	"golang.org/x/sys/unix"
)

type Asset struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Bytes     int64  `json:"bytes"`
	CreatedAt string `json:"created_at"`
}
type Catalog struct {
	Version        int      `json:"version"`
	Assets         []Asset  `json:"assets"`
	HiddenBuiltins []string `json:"hidden_builtins"`
}
type saved struct {
	Asset  Asset `json:"asset"`
	Reused bool  `json:"reused"`
}
type record struct {
	Asset
	owner, request, sha string
	originalBytes       int64
	deleted             sql.NullString
}

const columns = "id,name,width,height,bytes,created_at,owner,request_id,content_sha,original_bytes,deleted_at"

type scanner interface{ Scan(...any) error }

func scanRecord(s scanner) (record, error) {
	var r record
	err := s.Scan(&r.ID, &r.Name, &r.Width, &r.Height, &r.Bytes, &r.CreatedAt, &r.owner, &r.request, &r.sha, &r.originalBytes, &r.deleted)
	return r, err
}

type store struct {
	root string
	db   *sql.DB
	lock *os.File
	mu   sync.Mutex
}

func privateDir(path string) error {
	// Validate existing ancestors BEFORE creating anything through them.
	for p := path; ; p = filepath.Dir(p) {
		i, err := os.Lstat(p)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil && !i.IsDir() {
			return errors.New("unsafe directory ancestor")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	for p := path; ; p = filepath.Dir(p) {
		i, err := os.Lstat(p)
		if err != nil {
			return err
		}
		if !i.IsDir() {
			return errors.New("unsafe directory")
		}
		if p == filepath.Dir(p) {
			break
		}
	}
	i, err := os.Lstat(path)
	if err != nil {
		return err
	}
	var st unix.Stat_t
	if err = unix.Lstat(path, &st); err != nil {
		return err
	}
	if st.Uid != uint32(os.Geteuid()) || i.Mode().Perm()&0022 != 0 {
		return errors.New("unsafe directory ownership or permissions")
	}
	return os.Chmod(path, 0700)
}
func openPrivate(path string, flags int) (*os.File, error) {
	fd, err := unix.Open(path, flags|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	i, err := f.Stat()
	if err != nil || !i.Mode().IsRegular() {
		f.Close()
		return nil, errors.New("unsafe private file")
	}
	var st unix.Stat_t
	if err = unix.Fstat(fd, &st); err != nil || st.Uid != uint32(os.Geteuid()) || i.Mode().Perm()&0022 != 0 {
		f.Close()
		return nil, errors.New("unsafe private file permissions")
	}
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}
func syncDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
func newStore(root string) (s *store, err error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return nil, errors.New("icon root must be a clean absolute path")
	}
	for _, d := range []string{"", "originals", "previews", ".staging", "trash", "trash/originals", "trash/previews"} {
		if err = privateDir(filepath.Join(root, d)); err != nil {
			return nil, err
		}
	}
	lock, err := openPrivate(filepath.Join(root, ".native.lock"), unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		return nil, err
	}
	if err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("icon root is already open")
	}
	s = &store{root: root, lock: lock}
	defer func() {
		if err != nil {
			s.close()
		}
	}()
	// Refuse a nonempty asset tree without a catalog instead of interpreting legacy
	// or unknown data as disposable crash remnants.
	if _, e := os.Lstat(filepath.Join(root, "catalog.db")); os.IsNotExist(e) {
		for _, d := range []string{"originals", "previews", ".staging", "trash/originals", "trash/previews"} {
			entries, e := os.ReadDir(filepath.Join(root, d))
			if e != nil {
				return s, e
			}
			if len(entries) > 0 {
				return s, errors.New("private data exists without catalog")
			}
		}
	}
	// SQLite may create side files; pre-create database privately and validate any existing WAL/SHM.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		p := filepath.Join(root, "catalog.db"+suffix)
		if suffix != "" {
			if _, e := os.Lstat(p); os.IsNotExist(e) {
				continue
			}
		}
		f, e := openPrivate(p, unix.O_RDWR|unix.O_CREAT)
		if e != nil {
			return s, e
		}
		f.Close()
	}
	s.db, err = sql.Open("sqlite", filepath.Join(root, "catalog.db"))
	if err != nil {
		return s, err
	}
	s.db.SetMaxOpenConns(1)
	// An existing nonempty SQLite file must already be a recognized catalog.
	if info, e := os.Stat(filepath.Join(root, "catalog.db")); e != nil {
		return s, e
	} else if info.Size() > 0 {
		var n int
		if e = s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='assets'").Scan(&n); e != nil {
			return s, e
		}
		if n != 1 {
			return s, errors.New("unrecognized existing database")
		}
	}
	for _, q := range []string{"PRAGMA busy_timeout=2000", "PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", `CREATE TABLE IF NOT EXISTS assets (
 id TEXT PRIMARY KEY,owner TEXT NOT NULL,request_id TEXT NOT NULL,content_sha TEXT NOT NULL,name TEXT NOT NULL,
 width INTEGER NOT NULL CHECK(width=256),height INTEGER NOT NULL CHECK(height=256),bytes INTEGER NOT NULL CHECK(bytes>0),original_bytes INTEGER NOT NULL CHECK(original_bytes>0),created_at TEXT NOT NULL,deleted_at TEXT,UNIQUE(owner,request_id))`,
		`CREATE TABLE IF NOT EXISTS hidden_builtins (id TEXT PRIMARY KEY,deleted_at TEXT NOT NULL)`} {
		if _, err = s.db.Exec(q); err != nil {
			return s, err
		}
	}
	// Support original Python catalogs from before tombstones were introduced.
	rows, e := s.db.Query("PRAGMA table_info(assets)")
	if e != nil {
		return s, e
	}
	hasDeleted := false
	for rows.Next() {
		var cid, nn, pk int
		var name, typ string
		var def any
		if e = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); e != nil {
			rows.Close()
			return s, e
		}
		if name == "deleted_at" {
			hasDeleted = true
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return s, e
	}
	if !hasDeleted {
		if _, err = s.db.Exec("ALTER TABLE assets ADD COLUMN deleted_at TEXT"); err != nil {
			return s, err
		}
	}
	if err = s.validateSchema(); err != nil {
		return s, err
	}
	if err = s.recover(); err != nil {
		return s, err
	}
	if err = syncDir(root); err != nil {
		return s, err
	}
	return s, nil
}
func (s *store) close() error {
	var err error
	if s.db != nil {
		err = s.db.Close()
	}
	if s.lock != nil {
		s.lock.Close()
	}
	return err
}
func (s *store) path(dir, id, suffix string) string { return filepath.Join(s.root, dir, id+suffix) }
func (s *store) recover() error {
	rows, err := s.db.Query("SELECT " + columns + " FROM assets")
	if err != nil {
		return err
	}
	known := map[string]bool{}
	deleted := []record{}
	for rows.Next() {
		r, e := scanRecord(rows)
		if e != nil {
			rows.Close()
			return e
		}
		if !validRecord(r) {
			rows.Close()
			return errors.New("invalid catalog")
		}
		known[r.ID] = true
		if r.deleted.Valid {
			deleted = append(deleted, r)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, d := range []struct{ dir, suffix string }{{"originals", ".original"}, {"previews", ".webp"}, {".staging", ".tmp"}} {
		entries, e := os.ReadDir(filepath.Join(s.root, d.dir))
		if e != nil {
			return e
		}
		for _, entry := range entries {
			name := entry.Name()
			id := name[:max(0, len(name)-len(d.suffix))]
			if (d.dir == ".staging" && requestPattern.MatchString(id) || assetPattern.MatchString(id) && !known[id]) && name == id+d.suffix {
				if e = os.Remove(filepath.Join(s.root, d.dir, name)); e != nil {
					return e
				}
			}
		}
		if e = syncDir(filepath.Join(s.root, d.dir)); e != nil {
			return e
		}
	}
	for _, r := range deleted {
		if err = s.retire(r); err != nil {
			return err
		}
	}
	return nil
}
func (s *store) catalog() (Catalog, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := Catalog{1, []Asset{}, []string{}}
	rows, err := s.db.Query("SELECT " + columns + " FROM assets WHERE deleted_at IS NULL ORDER BY created_at,id LIMIT 201")
	if err != nil {
		return c, fault(503)
	}
	for rows.Next() {
		r, e := scanRecord(rows)
		if e != nil || !assetPattern.MatchString(r.ID) || r.Width != 256 || r.Height != 256 || r.Bytes <= 0 || len(c.Assets) >= MaxAssets {
			rows.Close()
			return c, fault(503)
		}
		c.Assets = append(c.Assets, r.Asset)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return c, fault(503)
	}
	rows, err = s.db.Query("SELECT id FROM hidden_builtins ORDER BY id")
	if err != nil {
		return c, fault(503)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if rows.Scan(&id) != nil || !builtinIDs[id] {
			return c, fault(503)
		}
		c.HiddenBuiltins = append(c.HiddenBuiltins, id)
	}
	if rows.Err() != nil {
		return c, fault(503)
	}
	return c, nil
}
func (s *store) check(r record, sha, name string) (saved, error) {
	if r.sha != sha || r.Name != name {
		return saved{}, fault(409)
	}
	if r.deleted.Valid {
		return saved{}, fault(410)
	}
	for _, v := range []struct {
		dir, suffix string
		size        int64
	}{{"originals", ".original", r.originalBytes}, {"previews", ".webp", r.Bytes}} {
		f, e := openPrivate(s.path(v.dir, r.ID, v.suffix), unix.O_RDONLY)
		if e != nil {
			return saved{}, fault(503)
		}
		i, e := f.Stat()
		f.Close()
		if e != nil || i.Size() != v.size {
			return saved{}, fault(503)
		}
	}
	return saved{r.Asset, true}, nil
}
func (s *store) retry(owner, key, name string, data []byte) (saved, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := scanRecord(s.db.QueryRow("SELECT "+columns+" FROM assets WHERE owner=? AND request_id=?", owner, key))
	if err == sql.ErrNoRows {
		return saved{}, false, nil
	}
	if err != nil {
		return saved{}, false, fault(503)
	}
	sha := fmt.Sprintf("%x", sha256.Sum256(data))
	v, e := s.check(r, sha, name)
	return v, true, e
}
func (s *store) stage(data []byte) (string, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	p := s.path(".staging", hex.EncodeToString(id[:]), ".tmp")
	f, err := openPrivate(p, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return "", err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	e := f.Close()
	if err == nil {
		err = e
	}
	if err != nil {
		os.Remove(p)
		return "", err
	}
	return p, nil
}
func (s *store) commit(owner, key, name string, original, preview []byte) (saved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	empty := saved{}
	sha := fmt.Sprintf("%x", sha256.Sum256(original))
	id := "upload-" + fmt.Sprintf("%x", sha256.Sum256([]byte(owner+":"+key)))[:32]
	tx, err := s.db.Begin()
	if err != nil {
		return empty, fault(503)
	}
	defer tx.Rollback()
	r, err := scanRecord(tx.QueryRow("SELECT "+columns+" FROM assets WHERE owner=? AND request_id=?", owner, key))
	if err == nil {
		return s.check(r, sha, name)
	}
	if err != sql.ErrNoRows {
		return empty, fault(503)
	}
	var count, total int64
	if err = tx.QueryRow("SELECT COUNT(*),COALESCE(SUM(original_bytes+bytes),0) FROM assets").Scan(&count, &total); err != nil {
		return empty, fault(503)
	}
	if count >= MaxAssets || total+int64(len(original)+len(preview)) > MaxStoredBytes {
		return empty, fault(507)
	}
	stages, installed := []string{}, []string{}
	commitAttempted := false
	defer func() {
		for _, p := range stages {
			os.Remove(p)
		}
		if !commitAttempted {
			for _, p := range installed {
				os.Remove(p)
			}
			syncDir(filepath.Join(s.root, "originals"))
			syncDir(filepath.Join(s.root, "previews"))
		}
	}()
	for i, data := range [][]byte{original, preview} {
		stage, e := s.stage(data)
		if e != nil {
			return empty, fault(503)
		}
		stages = append(stages, stage)
		dir, suffix := "originals", ".original"
		if i == 1 {
			dir, suffix = "previews", ".webp"
		}
		final := s.path(dir, id, suffix)
		if e = os.Link(stage, final); e != nil {
			return empty, fault(503)
		}
		installed = append(installed, final)
		if e = syncDir(filepath.Join(s.root, dir)); e != nil {
			return empty, fault(503)
		}
	}
	created := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	_, err = tx.Exec("INSERT INTO assets(id,owner,request_id,content_sha,name,width,height,bytes,original_bytes,created_at) VALUES(?,?,?,?,?,256,256,?,?,?)", id, owner, key, sha, name, len(preview), len(original), created)
	if err != nil {
		return empty, fault(503)
	}
	commitAttempted = true
	if err = tx.Commit(); err != nil {
		return empty, fault(503)
	}
	return saved{Asset{id, name, 256, 256, int64(len(preview)), created}, false}, nil
}
func (s *store) preview(id string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var size int64
	err := s.db.QueryRow("SELECT bytes FROM assets WHERE id=? AND deleted_at IS NULL", id).Scan(&size)
	if err == sql.ErrNoRows {
		return nil, fault(404)
	}
	if err != nil {
		return nil, fault(503)
	}
	f, err := openPrivate(s.path("previews", id, ".webp"), unix.O_RDONLY)
	if err != nil {
		return nil, fault(404)
	}
	defer f.Close()
	i, err := f.Stat()
	if err != nil || size <= 0 || size > MaxInputBytes || i.Size() != size {
		return nil, fault(404)
	}
	data, err := io.ReadAll(io.LimitReader(f, size+1))
	if err != nil || int64(len(data)) != size {
		return nil, fault(404)
	}
	return data, nil
}
func (s *store) retire(r record) error {
	for _, v := range []struct {
		dir, suffix string
		size        int64
	}{{"originals", ".original", r.originalBytes}, {"previews", ".webp", r.Bytes}} {
		src, dst := s.path(v.dir, r.ID, v.suffix), s.path("trash/"+v.dir, r.ID, v.suffix)
		f, err := openPrivate(src, unix.O_RDONLY)
		if os.IsNotExist(err) {
			f, err = openPrivate(dst, unix.O_RDONLY)
			if err != nil {
				return err
			}
			i, e := f.Stat()
			f.Close()
			if e != nil || i.Size() != v.size {
				return errors.New("missing private trash")
			}
			continue
		}
		if err != nil {
			return err
		}
		i, err := f.Stat()
		f.Close()
		if err != nil || i.Size() != v.size {
			return errors.New("invalid private asset")
		}
		if err = os.Link(src, dst); os.IsExist(err) {
			di, e := os.Lstat(dst)
			if e != nil || !os.SameFile(i, di) {
				return errors.New("private trash collision")
			}
		} else if err != nil {
			return err
		}
		if err = syncDir(filepath.Join(s.root, "trash", v.dir)); err != nil {
			return err
		}
		if err = os.Remove(src); err != nil {
			return err
		}
		if err = syncDir(filepath.Join(s.root, v.dir)); err != nil {
			return err
		}
	}
	return nil
}
func (s *store) delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	created := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	if builtinIDs[id] {
		_, err := s.db.Exec("INSERT INTO hidden_builtins(id,deleted_at) VALUES(?,?) ON CONFLICT(id) DO NOTHING", id, created)
		if err != nil {
			return fault(503)
		}
		return nil
	}
	r, err := scanRecord(s.db.QueryRow("SELECT "+columns+" FROM assets WHERE id=?", id))
	if err == sql.ErrNoRows {
		return fault(404)
	}
	if err != nil {
		return fault(503)
	}
	// Durable tombstone precedes all moves: crashes cannot make a deleted preview public.
	if _, err = s.db.Exec("UPDATE assets SET deleted_at=? WHERE id=? AND deleted_at IS NULL", created, id); err != nil {
		return fault(503)
	}
	if err = s.retire(r); err != nil {
		return fault(503)
	}
	return nil
}
