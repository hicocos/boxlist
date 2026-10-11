package iconlibrary

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEmptyStoreBeforeSeedInstallation(t *testing.T) {
	s, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	catalog, err := s.catalog()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"version":1,"assets":[]}` {
		t.Fatalf("unexpected empty catalog: %s", encoded)
	}
	var tables int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='hidden_builtins'").Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatal("new catalog contains obsolete visibility table")
	}
}

func TestCatalogMigrationPreservesUploads(t *testing.T) {
	for _, withoutTombstones := range []bool{false, true} {
		name := "native"
		if withoutTombstones {
			name = "python-before-tombstones"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			s, err := newStore(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if s != nil {
					s.close()
				}
			})
			original, preview := []byte("original upload bytes"), []byte("uploaded preview bytes")
			const key = "0123456789abcdef0123456789abcdef"
			asset, err := s.commit("1", key, "uploaded icon", original, preview)
			if err != nil {
				t.Fatal(err)
			}
			// Reconstruct both supported pre-cutover catalog formats.
			if _, err := s.db.Exec("CREATE TABLE hidden_builtins (id TEXT PRIMARY KEY,deleted_at TEXT NOT NULL)"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec("INSERT INTO hidden_builtins VALUES ('pack-folder','2026-10-01T00:00:00.000Z')"); err != nil {
				t.Fatal(err)
			}
			if withoutTombstones {
				if _, err := s.db.Exec("ALTER TABLE assets DROP COLUMN deleted_at"); err != nil {
					t.Fatal(err)
				}
			}
			// Opening again must migrate once and remain idempotent on restart.
			for range 2 {
				if err := s.close(); err != nil {
					t.Fatal(err)
				}
				s, err = newStore(root)
				if err != nil {
					t.Fatal(err)
				}
				var tables int
				if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='hidden_builtins'").Scan(&tables); err != nil {
					t.Fatal(err)
				}
				if tables != 0 {
					t.Fatal("obsolete visibility table survived migration")
				}
				catalog, err := s.catalog()
				if err != nil {
					t.Fatal(err)
				}
				if catalog.Version != 1 || len(catalog.Assets) != 1 || catalog.Assets[0] != asset.Asset {
					t.Fatalf("uploaded asset changed: %+v", catalog)
				}
				got, _, err := s.preview(asset.Asset.ID)
				if err != nil || !bytes.Equal(got, preview) {
					t.Fatalf("preview changed: %q, %v", got, err)
				}
				got, err = os.ReadFile(filepath.Join(root, "originals", asset.Asset.ID+".original"))
				if err != nil || !bytes.Equal(got, original) {
					t.Fatalf("original changed: %q, %v", got, err)
				}
				reused, exists, err := s.retry("1", key, "uploaded icon", original)
				if err != nil || !exists || !reused.Reused || reused.Asset != asset.Asset {
					t.Fatalf("upload retry changed: %+v, %v, %v", reused, exists, err)
				}
			}
		})
	}
}

func TestCatalogMigrationPreservesDeletedUploads(t *testing.T) {
	root := t.TempDir()
	s, err := newStore(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if s != nil {
			s.close()
		}
	})
	const key = "0123456789abcdef0123456789abcdef"
	original := []byte("deleted original")
	asset, err := s.commit("1", key, "deleted icon", original, []byte("deleted preview"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.delete(asset.Asset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TABLE hidden_builtins (id TEXT PRIMARY KEY,deleted_at TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	s, err = newStore(root)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := s.catalog()
	if err != nil || len(catalog.Assets) != 0 {
		t.Fatalf("deleted upload became visible: %+v, %v", catalog, err)
	}
	_, exists, err := s.retry("1", key, "deleted icon", original)
	if f, ok := err.(Fault); !ok || f.Status != 410 || !exists {
		t.Fatalf("deleted upload lost its tombstone: %v, %v", exists, err)
	}
	got, err := os.ReadFile(filepath.Join(root, "trash", "originals", asset.Asset.ID+".original"))
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("deleted original was not preserved: %q, %v", got, err)
	}
}
