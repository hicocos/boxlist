package derivatives

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	appdb "github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

func coreManager(t *testing.T) *Manager {
	t.Helper()
	t.Setenv("DERIVATIVES_ENABLED", "true")
	t.Setenv("DERIVATIVES_MIN_FREE_BYTES", "1")
	db, e := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "core.sqlite")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NamingStrategy: schema.NamingStrategy{TablePrefix: "test_"}})
	if e != nil {
		t.Fatal(e)
	}
	if e = db.AutoMigrate(&model.User{}, &model.Storage{}, &model.Meta{}); e != nil {
		t.Fatal(e)
	}
	sql, e := db.DB()
	if e != nil {
		t.Fatal(e)
	}
	sql.SetMaxOpenConns(1)
	m, e := New(db, filepath.Join(t.TempDir(), "derivatives"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Close(); sql.Close() })
	if e = db.Create(&model.User{ID: 1, Username: "admin", Role: model.ADMIN, BasePath: "/"}).Error; e != nil {
		t.Fatal(e)
	}
	return m
}
func coreStorage(t *testing.T, m *Manager) model.Storage {
	t.Helper()
	s := model.Storage{MountPath: "/pictures", Driver: "CoreTest", Addition: `{"root":"one"}`}
	if e := m.db.Create(&s).Error; e != nil {
		t.Fatal(e)
	}
	return s
}
func coreJob(t *testing.T, m *Manager, s model.Storage) Job {
	t.Helper()
	j := Job{Path: s.MountPath, Profiles: "thumb,reader", Creator: 1, StorageID: s.ID, Binding: Binding(&s), State: "queued"}
	if e := m.db.Create(&j).Error; e != nil {
		t.Fatal(e)
	}
	return j
}
func coreItem(t *testing.T, m *Manager, j Job, p string) Item {
	t.Helper()
	item := Item{JobID: j.ID, Path: p, PathKey: digest(p), Parent: j.Path, Kind: "file", State: "active", Stage: "encoding", Profiles: j.Profiles, Eligible: true, Attempts: 1}
	if e := m.db.Create(&item).Error; e != nil {
		t.Fatal(e)
	}
	for _, profile := range strings.Split(j.Profiles, ",") {
		if e := m.db.Create(&DerivativeItemProfile{ItemID: item.ID, Profile: profile, State: "queued"}).Error; e != nil {
			t.Fatal(e)
		}
	}
	return item
}
func coreArtifact(t *testing.T, m *Manager, s model.Storage, p, rev, profile string) Artifact {
	t.Helper()
	src := newSource(&s, p, rev)
	if e := m.db.Transaction(func(tx *gorm.DB) error { return observeSources(tx, []DerivativeSource{src}) }); e != nil {
		t.Fatal(e)
	}
	data := []byte("local artifact fixture " + p + profile)
	sum := sha256.Sum256(data)
	id := digest(src.Key, rev, profile, hex.EncodeToString(sum[:]))
	a := Artifact{ID: id, SourceKey: src.Key, PathKey: src.PathKey, ParentKey: src.ParentKey, StorageID: s.ID, Binding: Binding(&s), Path: p, Revision: rev, Profile: profile, Version: ProfileVersion, State: "ready", Width: 1, Height: 1, Bytes: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Manual: true, File: "artifacts/" + id[:2] + "/" + id + ".webp"}
	if e := ensureDirectory(filepath.Dir(filepath.Join(m.root, a.File))); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(m.root, a.File), data, 0600); e != nil {
		t.Fatal(e)
	}
	if e := m.db.Create(&a).Error; e != nil {
		t.Fatal(e)
	}
	return a
}

func TestCoreIdentityAndScope(t *testing.T) {
	s := model.Storage{ID: 1, MountPath: "/pictures", Driver: "Cloud", Addition: `{"b":2,"a":1}`}
	same := s
	same.Addition = `{ "a": 1, "b": 2 }`
	if Binding(&s) != Binding(&same) {
		t.Fatal("JSON key order changed binding")
	}
	same.Addition = `{"a":2}`
	if Binding(&s) == Binding(&same) {
		t.Fatal("new source kept binding")
	}
	o := &model.Object{Name: "a.jpg", Size: 1, Modified: time.Unix(10, 0)}
	before := Revision(o)
	o.Size++
	if Revision(o) == before {
		t.Fatal("size change not versioned")
	}
	if digest(Binding(&s), "/pictures/A.jpg") == digest(Binding(&s), "/pictures/a.jpg") {
		t.Fatal("case-sensitive paths collapsed")
	}
	for _, p := range []string{"relative", "/a/../b", "/a/./b", "/a//b", "/a\\b", "/a\x00b"} {
		if _, e := canonicalPath(p); e == nil {
			t.Fatalf("accepted %q", p)
		}
	}
	if inside("/a", "/ab") {
		t.Fatal("prefix escaped subtree")
	}
	rows := []model.Storage{s, {ID: 2, Driver: "Alias", MountPath: "/pictures/alias"}, {ID: 3, Driver: "Cloud", MountPath: "/pictures/sub"}}
	if _, e := storageForPath(rows, "/pictures/alias/file.jpg"); e == nil {
		t.Fatal("alias accepted")
	}
	if got, e := storageForPath(rows, "/pictures/sub/file.jpg"); e != nil || got.ID != 3 {
		t.Fatalf("nested mount resolution: %v %v", got, e)
	}
}
func TestCorePermissionRevalidation(t *testing.T) {
	m := coreManager(t)
	ctx := context.Background()
	if e := m.checkPermission(ctx, 1, "/pictures/a.jpg"); e != nil {
		t.Fatal(e)
	}
	if e := m.db.Create(&model.Meta{Path: "/pictures", ReadUsers: []uint{2}, ReadUsersSub: true}).Error; e != nil {
		t.Fatal(e)
	}
	if !errors.Is(m.checkPermission(ctx, 1, "/pictures/a.jpg"), ErrPermission) {
		t.Fatal("administrator read restriction ignored")
	}
	m.db.Where("1=1").Delete(&model.Meta{})
	m.db.Model(&model.User{}).Where("id = ?", 1).Update("base_path", "/elsewhere")
	if !errors.Is(m.checkPermission(ctx, 1, "/pictures/a.jpg"), ErrPermission) {
		t.Fatal("base path ignored")
	}
	m.db.Model(&model.User{}).Where("id = ?", 1).Updates(map[string]any{"base_path": "/", "disabled": true})
	if _, e := m.creator(ctx, 1); !errors.Is(e, ErrPermission) {
		t.Fatal("disabled creator accepted")
	}
}
func TestCoreBrowseNeverIndexesUnrequestedSources(t *testing.T) {
	m := coreManager(t)
	coreStorage(t, m)
	objects := []model.Obj{&model.Object{Name: "unrequested.jpg", Size: 10, Modified: time.Unix(1, 0)}}
	if _, e := m.Lookup(context.Background(), "/pictures", objects); e != nil {
		t.Fatal(e)
	}
	var count int64
	if e := m.db.Model(&DerivativeSource{}).Count(&count).Error; e != nil || count != 0 {
		t.Fatal("ordinary browsing wrote generation index", count, e)
	}
}

func TestCoreIndexReadAndInvalidation(t *testing.T) {
	m := coreManager(t)
	s := coreStorage(t, m)
	obj := &model.Object{Name: "A.jpg", Size: 12, Modified: time.Unix(123, 0)}
	a := coreArtifact(t, m, s, "/pictures/A.jpg", Revision(obj), "thumb")
	out, e := m.Lookup(context.Background(), "/pictures", []model.Obj{obj})
	if e != nil || out["A.jpg"]["thumb"].ID != a.ID {
		t.Fatalf("lookup = %#v %v", out, e)
	}
	if _, e = m.Artifact(a.ID, "thumb"); e != nil {
		t.Fatal(e)
	}
	obj.Size++
	out, e = m.Lookup(context.Background(), "/pictures", []model.Obj{model.StampObjObservation(obj, time.Now())})
	if e != nil || len(out) != 0 {
		t.Fatalf("old revision returned: %v %v", out, e)
	}
	if _, e = m.Artifact(a.ID, "thumb"); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal("old ticket artifact remains ready")
	}
	var jobs int64
	m.db.Model(&Job{}).Count(&jobs)
	if jobs != 0 {
		t.Fatal("read enqueued generation")
	}
	b := coreArtifact(t, m, s, "/pictures/B.jpg", "b", "reader")
	s.Addition = `{"root":"two"}`
	m.db.Save(&s)
	out, e = m.LookupPaths(context.Background(), []string{"/pictures/B.jpg"})
	if e != nil || len(out) != 0 {
		t.Fatal("rebound source leaked artifact")
	}
	if _, e = m.Artifact(b.ID, "reader"); !errors.Is(e, gorm.ErrRecordNotFound) {
		t.Fatal("binding not checked")
	}
}
func TestCoreDisabledRetainsButDoesNotServe(t *testing.T) {
	m := coreManager(t)
	s := coreStorage(t, m)
	a := coreArtifact(t, m, s, "/pictures/a.jpg", "r", "thumb")
	m.cfg.Enabled = false
	if out, e := m.LookupPaths(context.Background(), []string{a.Path}); e != nil || len(out) != 0 {
		t.Fatal(out, e)
	}
	if out, e := m.Lookup(context.Background(), "/pictures", []model.Obj{&model.Object{Name: "a.jpg"}}); e != nil || len(out) != 0 {
		t.Fatal(out, e)
	}
	if _, e := m.Artifact(a.ID, "thumb"); !errors.Is(e, ErrDisabled) {
		t.Fatal(e)
	}
	if _, e := m.Submit(context.Background(), 1, SubmitRequest{Path: "/pictures"}); !errors.Is(e, ErrDisabled) {
		t.Fatal(e)
	}
	if _, e := m.FilePath(a); e != nil {
		t.Fatal("disabled switch deleted result", e)
	}
}
func TestCoreRecoveryAndSafeCleanup(t *testing.T) {
	m := coreManager(t)
	s := coreStorage(t, m)
	j := coreJob(t, m, s)
	item := coreItem(t, m, j, "/pictures/a.jpg")
	m.db.Model(&Job{}).Where("id = ?", j.ID).Update("state", "processing")
	m.db.Model(&DerivativeItemProfile{}).Where("item_id = ?", item.ID).Update("state", "active")
	keep := coreArtifact(t, m, s, "/pictures/keep.jpg", "r", "thumb")
	missing := coreArtifact(t, m, s, "/pictures/missing.jpg", "r", "thumb")
	os.Remove(filepath.Join(m.root, missing.File))
	if e := m.recover(); e != nil {
		t.Fatal(e)
	}
	var got Item
	m.db.First(&got, item.ID)
	if got.State != "queued" {
		t.Fatal(got.State)
	}
	var a Artifact
	m.db.First(&a, "id = ?", missing.ID)
	if a.State != "missing" {
		t.Fatal(a.State)
	}
	old := filepath.Join(m.root, "work", "item-999999-interrupted")
	os.MkdirAll(old, 0700)
	os.WriteFile(filepath.Join(old, "input"), []byte("incomplete"), 0600)
	past := time.Now().Add(-48 * time.Hour)
	os.Chtimes(old, past, past)
	fresh := filepath.Join(m.root, "work", "item-999998-fresh")
	os.MkdirAll(fresh, 0700)
	if _, e := m.Cleanup("temporary"); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(old); !os.IsNotExist(e) {
		t.Fatal("expired work retained")
	}
	if _, e := os.Stat(fresh); e != nil {
		t.Fatal("fresh work deleted")
	}
	if _, e := m.Cleanup("stale"); e != nil {
		t.Fatal(e)
	}
	if _, e := m.FilePath(keep); e != nil {
		t.Fatal("current manual result deleted", e)
	}
}
func TestCoreFilePathRejectsEscapes(t *testing.T) {
	m := coreManager(t)
	s := coreStorage(t, m)
	a := coreArtifact(t, m, s, "/pictures/a.jpg", "r", "thumb")
	for _, p := range []string{"../../etc/passwd", "/etc/passwd", "artifacts/../x", "artifacts\\x", "artifacts/xx/wrong.webp"} {
		bad := a
		bad.File = p
		if _, e := m.FilePath(bad); e == nil {
			t.Fatal("accepted", p)
		}
	}
	p := filepath.Join(m.root, a.File)
	os.Remove(p)
	target := filepath.Join(t.TempDir(), "outside")
	os.WriteFile(target, []byte("private"), 0600)
	if e := os.Symlink(target, p); e != nil {
		t.Skip(e)
	}
	if _, e := m.FilePath(a); e == nil {
		t.Fatal("followed symlink")
	}
}
func TestCoreProfileRetryAndDiskPause(t *testing.T) {
	m := coreManager(t)
	s := coreStorage(t, m)
	j := coreJob(t, m, s)
	item := coreItem(t, m, j, "/pictures/a.jpg")
	a := coreArtifact(t, m, s, item.Path, "rev", "thumb")
	m.db.Model(&DerivativeItemProfile{}).Where("item_id = ? AND profile = ?", item.ID, "thumb").Updates(map[string]any{"state": "ready", "artifact_id": a.ID})
	m.failItem(j, item, ErrDisk)
	got, _ := m.Job(strconv.Itoa(int(j.ID)))
	if got.State != "paused" {
		t.Fatal(got.State)
	}
	var it Item
	m.db.First(&it, item.ID)
	if it.Attempts != 0 || it.State != "queued" {
		t.Fatalf("disk consumed retry: %+v", it)
	}
	m.db.Model(&Item{}).Where("id = ?", item.ID).Update("state", "partial_failed")
	m.db.Model(&DerivativeItemProfile{}).Where("item_id = ? AND profile = ?", item.ID, "reader").Updates(map[string]any{"state": "failed", "attempts": 3})
	if e := m.Control(j.ID, "retry_failed"); e != nil {
		t.Fatal(e)
	}
	var profiles []DerivativeItemProfile
	m.db.Where("item_id = ?", item.ID).Order("id").Find(&profiles)
	if profiles[0].State != "ready" || profiles[1].State != "queued" || profiles[1].Attempts != 0 {
		t.Fatalf("retry overwrote good profile: %+v", profiles)
	}
	rows, total, e := m.Items(strconv.Itoa(int(j.ID)), 1, 10, "")
	if e != nil || total != 1 || len(rows[0].Result) != 2 {
		t.Fatal(rows, total, e)
	}
}
func TestCoreStatsAndJSONContract(t *testing.T) {
	m := coreManager(t)
	s := coreStorage(t, m)
	j := coreJob(t, m, s)
	item := coreItem(t, m, j, "/pictures/a.jpg")
	m.db.Model(&Item{}).Where("id = ?", item.ID).Updates(map[string]any{"state": "partial_failed", "downloaded_bytes": 10, "generated_bytes": 4})
	m.db.Model(&DerivativeItemProfile{}).Where("item_id = ? AND profile = ?", item.ID, "thumb").Update("state", "ready")
	m.db.Model(&DerivativeItemProfile{}).Where("item_id = ? AND profile = ?", item.ID, "reader").Update("state", "failed")
	got, e := m.Job(strconv.Itoa(int(j.ID)))
	if e != nil || got.Stats.PartialFiles != 1 || got.Stats.ArtifactsReady != 1 || got.Stats.ArtifactsFailed != 1 || got.Stats.DownloadedBytes != 10 {
		t.Fatal(got.Stats, e)
	}
	jobs, n, e := m.Jobs(1, 20)
	if e != nil || n != 1 || jobs[0].Stats.PartialFiles != 1 {
		t.Fatal(jobs, n, e)
	}
	b, e := json.Marshal(got)
	if e != nil {
		t.Fatal(e)
	}
	var wire map[string]any
	json.Unmarshal(b, &wire)
	if _, ok := wire["profiles"].([]any); !ok {
		t.Fatal(string(b))
	}
	rule := Rule{Profiles: "thumb,reader", Excludes: "**/skip/**\n*.tmp", IntervalMinutes: 360}
	b, e = json.Marshal(rule)
	if e != nil {
		t.Fatal(e)
	}
	json.Unmarshal(b, &wire)
	if wire["interval_hours"] != float64(6) {
		t.Fatal(string(b))
	}
	if _, ok := wire["excludes"].([]any); !ok {
		t.Fatal(string(b))
	}
}
func TestCoreRetryAfterAndSafeErrors(t *testing.T) {
	now := time.Unix(1700000000, 0)
	if retryAfter("120", now) != 2*time.Minute {
		t.Fatal("seconds Retry-After")
	}
	if retryAfter(now.Add(time.Minute).UTC().Format(http.TimeFormat), now) != time.Minute {
		t.Fatal("date Retry-After")
	}
	e := &sourceHTTPError{Status: 429, After: time.Minute}
	if !retryable(e) || retryDelay(1, e) < time.Minute {
		t.Fatal("429 not respected")
	}
	if retryable(&sourceHTTPError{Status: 403}) {
		t.Fatal("403 retried")
	}
	secret := errors.New("https://user:password@cloud.example/a?token=secret")
	if strings.Contains(ErrorCode(secret), "secret") || strings.Contains(ErrorCode(secret), "http") {
		t.Fatal("error leaked credentials")
	}
	oldConf := conf.Conf
	conf.Conf = &conf.Config{}
	defer func() { conf.Conf = oldConf }()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("Retry-After", "60"); w.WriteHeader(429) }))
	defer srv.Close()
	_, err := openSource(context.Background(), &model.Link{URL: srv.URL}, 1)
	var he *sourceHTTPError
	if !errors.As(err, &he) || he.After != time.Minute {
		t.Fatalf("HTTP metadata lost: %v", err)
	}
}
func TestCoreRuleExcludes(t *testing.T) {
	x, e := validateExcludes([]string{"secret/**", "*.tmp", "cache/"})
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{"/pictures/secret/a.jpg", "/pictures/a.tmp", "/pictures/cache/a.jpg"} {
		if !excluded("/pictures", p, x) {
			t.Fatal(p)
		}
	}
	if excluded("/pictures", "/pictures/good.jpg", x) {
		t.Fatal("good file excluded")
	}
	if _, e = validateExcludes([]string{"../escape"}); e == nil {
		t.Fatal("accepted traversal pattern")
	}
}

// A fake driver still goes through the real op/fs registry, complete listing
// contract, GORM checkpoints and manager. No encoder stubs are installed.
type coreFakeDriver struct {
	model.Storage
	driver.RootPath
	calls   atomic.Int64
	objects []model.Obj
	listErr error
	entered chan struct{}
	release chan struct{}
}

func (d *coreFakeDriver) Config() driver.Config {
	return driver.Config{Name: "CoreTest", NoCache: true}
}
func (d *coreFakeDriver) GetAddition() driver.Additional { return &d.RootPath }
func (d *coreFakeDriver) Init(context.Context) error     { return nil }
func (d *coreFakeDriver) Drop(context.Context) error     { return nil }
func (d *coreFakeDriver) Get(ctx context.Context, p string) (model.Obj, error) {
	return &model.Object{ID: p, Path: p, Name: filepath.Base(p), IsFolder: true}, nil
}
func (d *coreFakeDriver) List(ctx context.Context, _ model.Obj, _ model.ListArgs) ([]model.Obj, error) {
	d.calls.Add(1)
	if d.entered != nil {
		select {
		case d.entered <- struct{}{}:
		default:
		}
	}
	if d.release != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-d.release:
		}
	}
	return d.objects, d.listErr
}
func (d *coreFakeDriver) Link(context.Context, model.Obj, model.LinkArgs) (*model.Link, error) {
	return nil, errors.New("unexpected link request")
}

func coreRegistered(t *testing.T, m *Manager, d *coreFakeDriver) model.Storage {
	t.Helper()
	mount := "/core-test-" + digest(t.Name())[:12]
	s := model.Storage{ID: uint(time.Now().UnixNano() & 0x7fffffff), Driver: "CoreTest", MountPath: mount, Addition: "{}"}
	// Use supported driver registration/loading rather than mutating private op
	// package globals. Tests are deliberately sequential because op owns a registry.
	oldConf := conf.Conf
	conf.Conf = &conf.Config{}
	t.Cleanup(func() { conf.Conf = oldConf })
	appdb.Init(m.db)
	op.RegisterDriver(func() driver.Driver { return d })
	if e := m.db.Create(&s).Error; e != nil {
		t.Fatal(e)
	}
	if e := op.LoadStorage(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	if e := m.db.First(&s, s.ID).Error; e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { op.DeleteStorageById(context.Background(), s.ID) })
	return s
}
