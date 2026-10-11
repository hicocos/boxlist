package derivatives

import (
	"context"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"testing"
	"time"
)

func TestOlderCachedListCannotRollbackGeneratedRevision(t *testing.T) {
	m := coreManager(t)
	storage := coreStorage(t, m)
	older := model.StampObjObservation(&model.Object{Name: "page.jpg", Size: 1, Modified: time.Unix(10, 0)}, time.Now().Add(-time.Minute))
	newer := &model.Object{Name: "page.jpg", Size: 2, Modified: time.Unix(20, 0)}
	a := coreArtifact(t, m, storage, "/pictures/page.jpg", Revision(newer), "reader")
	for i := 0; i < 3; i++ {
		out, e := m.Lookup(context.Background(), "/pictures", []model.Obj{older})
		if e != nil || out["page.jpg"]["reader"].ID != a.ID {
			t.Fatal("cached list revoked newest preview", out, e)
		}
	}
	if _, e := m.Artifact(a.ID, "reader"); e != nil {
		t.Fatal("valid ticket revoked", e)
	}
	var stored DerivativeSource
	if e := m.db.First(&stored, "source_key = ?", a.SourceKey).Error; e != nil || stored.Revision != Revision(newer) {
		t.Fatal("revision rolled back", stored, e)
	}
	// Even a differing object without provenance must not overwrite known state.
	if _, e := m.Lookup(context.Background(), "/pictures", []model.Obj{&model.Object{Name: "page.jpg", Size: 7}}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Artifact(a.ID, "reader"); e != nil {
		t.Fatal("unknown-age observation revoked preview", e)
	}
	// A later refresh may legitimately report an older source mtime/version.
	fresh := model.StampObjObservation(&model.Object{Name: "page.jpg", Size: 3, Modified: time.Unix(5, 0)}, time.Now().Add(time.Second))
	out, e := m.Lookup(context.Background(), "/pictures", []model.Obj{fresh})
	if e != nil || len(out) != 0 {
		t.Fatal("new authoritative change ignored", out, e)
	}
}
