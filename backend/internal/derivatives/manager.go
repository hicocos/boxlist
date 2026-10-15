package derivatives

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"gorm.io/gorm"
)

var Default *Manager

// Config is validated once at startup. Runtime concurrency lives separately
// under gate; changes affect new claims, never in-flight files.
type Config struct {
	Enabled                bool
	BudgetBytes            int64 // Zero means unlimited local derivative storage.
	MinFreeBytes           int64
	MaxJobs                int64
	MaxItems               int64
	MaxRules               int64
	MaxDepth               int
	FileTimeout            time.Duration
	ScanTimeout            time.Duration
	DownloadBytesPerSecond int64
	ScanIntervalMillis     int64
	RunStartHour           int64
	RunEndHour             int64
}

func configFromEnv() (Config, error) {
	c := Config{Enabled: true, BudgetBytes: 0, MinFreeBytes: 5 << 30, MaxJobs: 1000, MaxItems: 100000, MaxRules: 100, MaxDepth: 128, FileTimeout: 3 * time.Minute, ScanTimeout: 90 * time.Second, DownloadBytesPerSecond: 8 << 20, ScanIntervalMillis: 250}
	if v, ok := os.LookupEnv("DERIVATIVES_ENABLED"); ok {
		b, e := strconv.ParseBool(v)
		if e != nil {
			return c, fmt.Errorf("DERIVATIVES_ENABLED: %w", e)
		}
		c.Enabled = b
	}
	for _, v := range []struct {
		name     string
		dst      *int64
		min, max int64
	}{
		{"DERIVATIVES_BUDGET_BYTES", &c.BudgetBytes, 0, 1 << 50},
		{"DERIVATIVES_MIN_FREE_BYTES", &c.MinFreeBytes, 1, 1 << 50},
		{"DERIVATIVES_MAX_JOBS", &c.MaxJobs, 1, 10000},
		{"DERIVATIVES_MAX_ITEMS", &c.MaxItems, 1, 1000000},
		{"DERIVATIVES_MAX_RULES", &c.MaxRules, 1, 1000},
		{"DERIVATIVES_DOWNLOAD_BYTES_PER_SECOND", &c.DownloadBytesPerSecond, 64 << 10, 128 << 20},
		{"DERIVATIVES_SCAN_INTERVAL_MS", &c.ScanIntervalMillis, 100, 60000},
		{"DERIVATIVES_RUN_START_HOUR", &c.RunStartHour, 0, 23},
		{"DERIVATIVES_RUN_END_HOUR", &c.RunEndHour, 0, 23},
	} {
		if s, ok := os.LookupEnv(v.name); ok {
			n, e := strconv.ParseInt(s, 10, 64)
			if e != nil || n < v.min || n > v.max {
				return c, fmt.Errorf("invalid %s", v.name)
			}
			*v.dst = n
		}
	}
	if c.BudgetBytes > 0 && c.BudgetBytes < workReserve {
		return c, fmt.Errorf("invalid DERIVATIVES_BUDGET_BYTES")
	}
	return c, nil
}

type Manager struct {
	db        *gorm.DB
	root      string
	cfg       Config
	ctx       context.Context
	cancel    context.CancelFunc
	once      sync.Once
	closeOnce sync.Once
	wg        sync.WaitGroup
	wake      chan struct{}
	events    chan string
	// gate serializes claims/control/transactions without ever covering network IO.
	gate               sync.Mutex
	verifying          bool
	workerLimit        int
	activeClaims       map[uint]workerClaim
	fileTurn           bool
	diskMu             sync.Mutex
	diskAt             time.Time
	diskUsed, diskWork int64
	diskErr            error
	encoderMu          sync.RWMutex
	encoderError       string
	remoteReads        chan struct{}
	remoteCleanupMu    sync.RWMutex
	paceMu             sync.Mutex
	paceNext           time.Time
}

type ownScanKey struct{}

var hookManagers sync.Map
var hookOnce sync.Once

// New migrates additive derivative tables and recovers interrupted task state.
// Existing artifacts are checked only on access or by an explicit Verify request.
// Call Start after storage drivers have been loaded; Open is the auto-start variant.
func New(database *gorm.DB, root string) (*Manager, error) {
	if database == nil {
		return nil, fmt.Errorf("nil derivative database")
	}
	cfg, err := configFromEnv()
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err = ensureDirectory(root); err != nil {
		return nil, err
	}
	for _, dir := range []string{"work", "artifacts"} {
		if err = ensureDirectory(filepath.Join(root, dir)); err != nil {
			return nil, err
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{db: database, root: root, cfg: cfg, ctx: ctx, cancel: cancel, workerLimit: 1, activeClaims: make(map[uint]workerClaim), wake: make(chan struct{}, 4), events: make(chan string, 128), remoteReads: make(chan struct{}, 4)}
	if err = database.AutoMigrate(&DerivativeJob{}, &DerivativeItem{}, &DerivativeItemProfile{}, &DerivativeSource{}, &DerivativeArtifact{}, &DerivativeRule{}, &DerivativeS3Target{}, &DerivativeS3Setting{}, &DerivativeRemoteObject{}); err != nil {
		cancel()
		return nil, err
	}
	if err = database.Model(&Artifact{}).Where("state = ?", "cleaning").Update("state", "obsolete").Error; err != nil {
		cancel()
		return nil, err
	}
	if err = m.initWorkerSettings(); err != nil {
		cancel()
		return nil, err
	}
	if err = m.initS3Configuration(); err != nil {
		cancel()
		return nil, err
	}
	m.encoderError = "not_checked"
	if err = m.recover(); err != nil {
		cancel()
		return nil, err
	}
	return m, nil
}

func Open(database *gorm.DB, root string) (*Manager, error) {
	m, e := New(database, root)
	if e == nil {
		m.Start()
	}
	return m, e
}

func (m *Manager) Start() {
	m.once.Do(func() {
		m.gate.Lock()
		defer m.gate.Unlock()
		if m.ctx.Err() != nil {
			return
		}
		hookOnce.Do(func() {
			op.RegisterFileMutationHook(func(ctx context.Context, id uint, p string) {
				hookManagers.Range(func(k, v any) bool { _ = k.(*Manager).Mutated(context.WithoutCancel(ctx), id, p); return true })
			})
			op.RegisterObjsUpdateHook(func(ctx context.Context, parent string, _ []model.Obj) {
				if ctx.Value(ownScanKey{}) != nil {
					return
				}
				hookManagers.Range(func(k, v any) bool { k.(*Manager).Notify(parent); return true })
			})
		})
		hookManagers.Store(m, struct{}{})
		m.wg.Add(1)
		go m.loop()
	})
}

func (m *Manager) Close() {
	m.closeOnce.Do(func() {
		// Serialize cancellation with every wg.Add, including Start and step.
		m.gate.Lock()
		hookManagers.Delete(m)
		m.cancel()
		for _, claim := range m.activeClaims {
			claim.Cancel()
		}
		m.gate.Unlock()
		m.wg.Wait()
	})
}
func (m *Manager) signal() {
	// Coalesce a full pool's wakeups without blocking control or completion.
	for n := 0; n < cap(m.wake); n++ {
		select {
		case m.wake <- struct{}{}:
		default:
			return
		}
	}
}
func (m *Manager) Notify(parent string) {
	if m.ctx.Err() != nil {
		return
	}
	select {
	case m.events <- parent:
		m.signal()
	default:
	}
}

func canonicalPath(p string) (string, error) {
	if p == "" || len(p) > 4096 || p[0] != '/' || strings.ContainsAny(p, "\\\x00\r\n") {
		return "", ErrInvalidPath
	}
	for _, s := range strings.Split(p, "/") {
		if s == "." || s == ".." {
			return "", ErrInvalidPath
		}
	}
	// Repeated separators are not alternate identities.
	if strings.Contains(p, "//") {
		return "", ErrInvalidPath
	}
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	return p, nil
}
func validName(s string) bool {
	return s != "" && s != "." && s != ".." && len(s) <= 1024 && !strings.ContainsAny(s, "/\\\x00\r\n")
}
func normalizeProfiles(s string) (string, error) {
	if s == "" {
		return "thumb,reader", nil
	}
	thumb, reader := false, false
	for _, p := range strings.Split(s, ",") {
		switch p {
		case "thumb":
			if thumb {
				return "", ErrInvalidProfiles
			}
			thumb = true
		case "reader":
			if reader {
				return "", ErrInvalidProfiles
			}
			reader = true
		default:
			return "", ErrInvalidProfiles
		}
	}
	if thumb && reader {
		return "thumb,reader", nil
	}
	if thumb {
		return "thumb", nil
	}
	if reader {
		return "reader", nil
	}
	return "", ErrInvalidProfiles
}
func inside(parent, p string) bool {
	return parent == "/" || p == parent || strings.HasPrefix(p, parent+"/")
}
func pageBounds(page, per int) (int, int) {
	if page < 1 {
		page = 1
	}
	if per < 1 {
		per = 50
	}
	if per > 200 {
		per = 200
	}
	if page > 1000000 {
		page = 1000000
	}
	return (page - 1) * per, per
}

func (m *Manager) recover() error {
	// Deployments must not run two backend processes against the same derivatives
	// root. Recover task state before exposing the manager to API handlers.
	return m.db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(&Item{}).Where("state = ?", "active").Updates(map[string]any{"state": "queued", "stage": "recovered", "lease_until": time.Time{}}).Error; e != nil {
			return e
		}
		if e := tx.Model(&DerivativeItemProfile{}).Where("state = ?", "active").Update("state", "queued").Error; e != nil {
			return e
		}
		if e := tx.Model(&Job{}).Where("state IN ?", []string{"scanning", "processing"}).Update("state", "queued").Error; e != nil {
			return e
		}
		return tx.Model(&Job{}).Where("state = ?", "cancelling").Updates(map[string]any{"state": "cancelled", "active_key": nil}).Error
	})
}

type VerificationResult struct {
	Checked int64 `json:"checked"`
	Missing int64 `json:"missing"`
}

// Verify is an explicit administrator operation, never part of startup. It
// checks local bytes and remote metadata only, without fetching bucket objects.
func (m *Manager) Verify(ctx context.Context) (VerificationResult, error) {
	result := VerificationResult{}
	m.gate.Lock()
	if m.ctx.Err() != nil {
		m.gate.Unlock()
		return result, ErrClosed
	}
	if m.verifying {
		m.gate.Unlock()
		return result, ErrConflict
	}
	m.verifying = true
	m.wg.Add(1)
	m.gate.Unlock()
	defer func() {
		m.gate.Lock()
		m.verifying = false
		m.gate.Unlock()
		m.wg.Done()
	}()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	stop := context.AfterFunc(m.ctx, cancel)
	defer stop()
	defer cancel()
	database := m.db.WithContext(ctx)
	var cursor string
	rows := make([]Artifact, 0, 200)
	targetIDs := make([]uint, 0, 200)
	targets := make(map[uint]*DerivativeS3Target)
	loaded := make([]DerivativeS3Target, 0, 200)
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := database.Where("state = ? AND id > ?", "ready", cursor).Order("id").Limit(200).Find(&rows).Error; err != nil {
			return result, err
		}
		if len(rows) == 0 {
			break
		}
		targetIDs = targetIDs[:0]
		clear(targets)
		for _, a := range rows {
			if a.OutputTargetID != 0 {
				if _, seen := targets[a.OutputTargetID]; !seen {
					targetIDs = append(targetIDs, a.OutputTargetID)
					targets[a.OutputTargetID] = nil
				}
			}
		}
		if len(targetIDs) > 0 {
			if err := database.Select("id", "prefix").Where("id IN ?", targetIDs).Find(&loaded).Error; err != nil {
				// A database failure is not evidence of a missing artifact.
				return result, err
			}
			for i := range loaded {
				targets[loaded[i].ID] = &loaded[i]
			}
		}
		for _, a := range rows {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			cursor = a.ID
			valid := false
			if a.OutputTargetID != 0 {
				valid = remoteArtifactValid(a, targets[a.OutputTargetID])
			} else {
				valid = m.localArtifactFileValid(ctx, a, true)
			}
			if err := ctx.Err(); err != nil {
				return result, err
			}
			result.Checked++
			if !valid {
				// Never overwrite a concurrent publication or revoked state.
				updated := database.Model(&Artifact{}).Where("id = ? AND state = ? AND updated_at = ?", a.ID, "ready", a.UpdatedAt).
					Updates(map[string]any{"state": "missing", "error": "local_artifact_missing_or_corrupt"})
				if updated.Error != nil {
					return result, updated.Error
				}
				result.Missing += updated.RowsAffected
			}
		}
	}
	return result, nil
}
