package derivatives

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func budgetSparseFile(t *testing.T, m *Manager, directory string, size int64) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(m.root, directory, "budget-fixture"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(size) // Exercise real accounting without allocating 20+ GiB.
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	m.invalidateDisk()
}

func TestDerivativeStorageBudgetConfiguration(t *testing.T) {
	for _, entry := range []struct {
		name  string
		value string
		want  int64
		valid bool
	}{
		{"default", "", 0, true},
		{"unlimited", "0", 0, true},
		{"minimum", strconv.FormatInt(workReserve, 10), workReserve, true},
		{"maximum", strconv.FormatInt(1<<50, 10), 1 << 50, true},
		{"negative", "-1", 0, false},
		{"too_small", "1", 0, false},
		{"below_reservation", strconv.FormatInt(workReserve-1, 10), 0, false},
		{"too_large", strconv.FormatInt((1<<50)+1, 10), 0, false},
		{"not_a_number", "unlimited", 0, false},
		{"empty", "", 0, false},
	} {
		t.Run(entry.name, func(t *testing.T) {
			t.Setenv("DERIVATIVES_BUDGET_BYTES", entry.value)
			if entry.name == "default" {
				if err := os.Unsetenv("DERIVATIVES_BUDGET_BYTES"); err != nil {
					t.Fatal(err)
				}
			}
			cfg, err := configFromEnv()
			if !entry.valid {
				if err == nil {
					t.Fatal("invalid storage budget accepted")
				}
				return
			}
			if err != nil || cfg.BudgetBytes != entry.want {
				t.Fatal(cfg.BudgetBytes, err)
			}
		})
	}
}

func TestUnlimitedDerivativeStorageBeyondFormerBudget(t *testing.T) {
	t.Setenv("DERIVATIVES_BUDGET_BYTES", "0")
	m := coreManager(t)
	const used = int64(21 << 30)
	const work = int64(1 << 20)
	budgetSparseFile(t, m, "artifacts", used)
	budgetSparseFile(t, m, "work", work)
	for _, reserve := range []int64{0, 64 << 20, workReserve, workReserve * 4} {
		if err := m.diskAvailable(reserve); err != nil {
			t.Fatalf("unlimited budget rejected reservation %d beyond 20 GiB: %v", reserve, err)
		}
	}
	for _, report := range []any{m.Health(), m.Storage()} {
		health := report.(map[string]any)
		if health["budget_bytes"] != int64(0) || health["used_bytes"] != used || health["work_bytes"] != work || health["status"] != "ok" {
			t.Fatal(health)
		}
	}
	m.cfg.BudgetBytes = 20 << 30
	if err := m.diskAvailable(0); !errors.Is(err, ErrDisk) {
		t.Fatal("explicit budget did not protect storage", err)
	}
	if health := m.Health().(map[string]any); health["status"] != "disk_protection" {
		t.Fatal(health)
	}
	m.cfg.BudgetBytes = used + work + workReserve
	if err := m.diskAvailable(workReserve); err != nil {
		t.Fatal("exact positive budget should fit", err)
	}
	m.cfg.BudgetBytes--
	if err := m.diskAvailable(workReserve); !errors.Is(err, ErrDisk) {
		t.Fatal("explicit budget ignored work or reservation", err)
	}
}

func TestUnlimitedDerivativeStoragePreservesDiskSafety(t *testing.T) {
	t.Setenv("DERIVATIVES_BUDGET_BYTES", "0")
	m := coreManager(t)
	if err := m.diskAvailable(0); err != nil {
		t.Fatal("fixture storage unavailable", err)
	}
	m.cfg.MinFreeBytes = 1 << 60
	if err := m.diskAvailable(0); !errors.Is(err, ErrDisk) {
		t.Fatal("unlimited budget bypassed minimum free space", err)
	}
	if health := m.Health().(map[string]any); health["status"] != "disk_protection" || health["budget_bytes"] != int64(0) {
		t.Fatal(health)
	}
	m.cfg.MinFreeBytes = 1
	if err := m.diskAvailable(1 << 60); !errors.Is(err, ErrDisk) {
		t.Fatal("unlimited budget bypassed free-space reservation", err)
	}
}

func TestS3DerivativeStorageUsesOnlyTemporaryDiskSpace(t *testing.T) {
	t.Setenv("DERIVATIVES_BUDGET_BYTES", "0")
	m := coreManager(t)
	_, server := newS3TestStore(t)
	if _, err := m.SaveS3Configuration(context.Background(), s3TestRequest(server)); err != nil {
		t.Fatal(err)
	}
	m.cfg.MinFreeBytes = 5 << 30
	target, minFree, err := m.storageDiskPolicy()
	if err != nil || target != "s3" || minFree != 0 {
		t.Fatal(target, minFree, err)
	}
	// Deterministic disk snapshots cover a host below 5 GiB, without filling
	// the test machine. Existing temporary files are already reflected in free.
	if err = m.checkDiskAvailable(21<<30, 128<<20, workReserve*4, 2<<30, minFree); err != nil {
		t.Fatal("S3 work blocked below the local 5 GiB floor", err)
	}
	if err = m.checkDiskAvailable(21<<30, 128<<20, workReserve, uint64(workReserve-1), minFree); !errors.Is(err, ErrDisk) {
		t.Fatal("S3 work ignored insufficient temporary space", err)
	}
	if err = m.checkDiskAvailable(0, 0, workReserve, uint64(workReserve), minFree); err != nil {
		t.Fatal("exact S3 temporary reservation should fit", err)
	}
	m.cfg.BudgetBytes = 20 << 30
	if err = m.checkDiskAvailable(21<<30, 128<<20, workReserve, 2<<30, minFree); !errors.Is(err, ErrDisk) {
		t.Fatal("S3 mode bypassed explicitly configured local storage budget", err)
	}
	m.cfg.BudgetBytes = 0
	m.cfg.MinFreeBytes = 1 << 60 // The real host is below this local floor.
	if err = m.diskAvailable(workReserve); err != nil {
		t.Fatal("S3 disk check applied local output floor", err)
	}
	if health := m.Health().(map[string]any); health["status"] != "ok" || health["storage_target"] != "s3" || health["effective_min_free_bytes"] != int64(0) || health["min_free_bytes"] != m.cfg.MinFreeBytes {
		t.Fatal(health)
	}
	if err = m.diskAvailable(1 << 60); !errors.Is(err, ErrDisk) {
		t.Fatal("S3 disk check bypassed actual temporary-space safety", err)
	}
	j := remoteTestJob(t, m, "processing")
	src := remoteTestSource(t, m, j, j.Path+"/budget.png", "revision")
	if a := remotePublishFixture(t, m, j, src, "thumb", []byte("S3 output below local disk floor")); a.OutputTargetID == 0 {
		t.Fatal("S3 publication fell back to local output")
	}
	if _, err = m.SaveS3Configuration(context.Background(), S3ConfigRequest{Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if err = m.diskAvailable(workReserve); !errors.Is(err, ErrDisk) {
		t.Fatal("local output lost its free-space floor after S3 disable", err)
	}
	if health := m.Health().(map[string]any); health["status"] != "disk_protection" || health["storage_target"] != "local" || health["effective_min_free_bytes"] != m.cfg.MinFreeBytes {
		t.Fatal(health)
	}
	if target, _, err = m.storageDiskPolicy(); err != nil || target != "local" {
		t.Fatal("disabled S3 target remained active", target, err)
	}
}

func TestDerivativeStoragePolicyFailsClosedForMissingS3Target(t *testing.T) {
	m := coreManager(t)
	if err := m.db.Create(&DerivativeS3Setting{ID: 1, Enabled: true, TargetID: 999}).Error; err != nil {
		t.Fatal(err)
	}
	if err := m.diskAvailable(workReserve); !errors.Is(err, ErrS3TargetUnavailable) {
		t.Fatal("missing S3 target silently selected local output", err)
	}
	if health := m.Health().(map[string]any); health["status"] != "storage_unavailable" {
		t.Fatal(health)
	}
}

func TestGenerationRunWindows(t *testing.T) {
	m := &Manager{}
	when := func(h int) time.Time { return time.Date(2020, 1, 1, h, 0, 0, 0, time.UTC) }
	if !m.inRunWindow(when(18)) {
		t.Fatal("default should allow whole day")
	}
	m.cfg.RunStartHour = 8
	m.cfg.RunEndHour = 16
	for h := 0; h < 24; h++ {
		if m.inRunWindow(when(h)) != (h >= 8 && h < 16) {
			t.Fatal(h)
		}
	}
	m.cfg.RunStartHour = 22
	m.cfg.RunEndHour = 6
	for h := 0; h < 24; h++ {
		if m.inRunWindow(when(h)) != (h >= 22 || h < 6) {
			t.Fatal(h)
		}
	}
}
func TestBoundedDownloadPaceAndCancellation(t *testing.T) {
	m := &Manager{cfg: Config{DownloadBytesPerSecond: 1 << 20}}
	started := time.Now()
	n, e := io.Copy(io.Discard, m.pacedSource(context.Background(), bytes.NewReader(make([]byte, 64<<10))))
	if e != nil || n != 64<<10 || time.Since(started) < 50*time.Millisecond {
		t.Fatal(n, e, time.Since(started))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = m.pacedSource(ctx, bytes.NewReader([]byte("source"))).Read(make([]byte, 64)); e != context.Canceled {
		t.Fatal(e)
	}
	if e = waitDerivativePace(ctx, time.Hour); e != context.Canceled {
		t.Fatal(e)
	}
}
func TestBudgetConfigRejectsUnsafeValues(t *testing.T) {
	for _, entry := range []struct{ k, v string }{{"DERIVATIVES_DOWNLOAD_BYTES_PER_SECOND", "0"}, {"DERIVATIVES_SCAN_INTERVAL_MS", "0"}, {"DERIVATIVES_RUN_END_HOUR", "24"}} {
		t.Run(entry.k, func(t *testing.T) {
			t.Setenv(entry.k, entry.v)
			if _, e := configFromEnv(); e == nil {
				t.Fatal("unsafe value accepted")
			}
		})
	}
}
