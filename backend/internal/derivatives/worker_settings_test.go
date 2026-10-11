package derivatives

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"testing"
)

func TestWorkerLimitDefaultBoundsPersistsAndFailedSaveUnchanged(t *testing.T) {
	m := coreManager(t)
	if v := m.WorkerConfiguration(); v.Workers != 1 || v.MinWorkers != 1 || v.MaxWorkers != 4 {
		t.Fatal(v)
	}
	for _, invalid := range []int{-1, 0, 5, 100} {
		if _, e := m.SaveWorkerConfiguration(context.Background(), invalid); e == nil {
			t.Fatal("accepted", invalid)
		}
	}
	for _, n := range []int{2, 3, 4, 1, 4} {
		v, e := m.SaveWorkerConfiguration(context.Background(), n)
		if e != nil || v.Workers != n {
			t.Fatal(v, e)
		}
	}
	reopened, e := New(m.db, m.root)
	if e != nil {
		t.Fatal(e)
	}
	defer reopened.Close()
	if reopened.WorkerConfiguration().Workers != 4 {
		t.Fatal("not persisted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = m.SaveWorkerConfiguration(ctx, 1); e == nil {
		t.Fatal("cancelled save succeeded")
	}
	if m.WorkerConfiguration().Workers != 4 {
		t.Fatal("cancelled save changed limit")
	}
	injected := errors.New("injected settings failure")
	if e = m.db.Callback().Create().Before("gorm:create").Register("test_worker_limit_fail", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "DerivativeWorkerSetting" {
			tx.AddError(injected)
		}
	}); e != nil {
		t.Fatal(e)
	}
	defer m.db.Callback().Create().Remove("test_worker_limit_fail")
	if _, e = m.SaveWorkerConfiguration(context.Background(), 1); e == nil {
		t.Fatal("failed persist accepted")
	}
	if m.WorkerConfiguration().Workers != 4 {
		t.Fatal("failed persist changed memory")
	}
}
