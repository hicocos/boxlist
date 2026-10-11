package iconlibrary

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const transparencyUploadKey = "0123456789abcdef0123456789abcdef"

func TestReplaceGIFIdentityRetryAndConflict(t *testing.T) {
	s, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	input := transparencyFixture(t)
	saved, err := s.commit("1", transparencyUploadKey, "original.gif", input, input)
	if err != nil {
		t.Fatal(err)
	}
	asset := saved.Asset
	output, changed, err := transparentGIF(input, nil)
	if err != nil || !changed {
		t.Fatalf("fixture processing: %v", err)
	}
	for name, revision := range map[string]string{"auth": asset.Revision, "conflict": strings.Repeat("0", 64)} {
		t.Run(name, func(t *testing.T) {
			authorize := func() error { return nil }
			if name == "auth" {
				authorize = func() error { return fault(403) }
			}
			if _, err := s.replaceGIF(asset.ID, revision, output, authorize); err == nil {
				t.Fatal("invalid replacement accepted")
			}
			for _, path := range []string{s.path("originals", asset.ID, ".original"), s.path("previews", asset.ID, ".gif")} {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, input) {
					t.Fatalf("failure changed bytes: %v", err)
				}
			}
		})
	}
	updated, err := s.replaceGIF(asset.ID, asset.Revision, output, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != asset.ID || updated.Name != asset.Name || updated.CreatedAt != asset.CreatedAt || updated.Revision == asset.Revision || updated.Revision != contentRevision(output) {
		t.Fatalf("identity/revision: %+v", updated)
	}
	for _, path := range []string{s.path("originals", asset.ID, ".original"), s.path("previews", asset.ID, ".gif")} {
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, output) {
			t.Fatalf("replacement mismatch: %v", err)
		}
	}
	for _, dir := range []string{".staging", "trash/originals", "trash/previews"} {
		entries, err := os.ReadDir(filepath.Join(s.root, dir))
		if err != nil || len(entries) != 0 {
			t.Fatalf("retained recovery bytes in %s: %v", dir, err)
		}
	}
	retry, found, err := s.retry("1", transparencyUploadKey, "original.gif", input)
	if err != nil || !found || !retry.Reused || retry.Asset != updated {
		t.Fatalf("retry reverted edited asset: %+v %v", retry, err)
	}
	if _, err := s.replaceGIF(asset.ID, asset.Revision, output, func() error { return nil }); err == nil {
		t.Fatal("stale revision accepted")
	}
	if err := s.delete(asset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.replaceGIF(asset.ID, updated.Revision, output, func() error { return nil }); err == nil {
		t.Fatal("deleted asset resurrected")
	}
}

func TestReplaceGIFRestartRollsForwardEveryCommitBoundary(t *testing.T) {
	for step := range 5 {
		t.Run(string(rune('0'+step)), func(t *testing.T) {
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
			input := transparencyFixture(t)
			asset, err := s.commit("1", transparencyUploadKey, "restart.gif", input, input)
			if err != nil {
				t.Fatal(err)
			}
			output, _, err := transparentGIF(input, nil)
			if err != nil {
				t.Fatal(err)
			}
			stage, err := s.stage(output)
			if err != nil {
				t.Fatal(err)
			}
			journal := replacementJournal{ID: asset.Asset.ID, Stage: filepath.Base(stage), Revision: contentRevision(output), Bytes: int64(len(output))}
			data, err := json.Marshal(journal)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, ".replacement"), data, 0600); err != nil {
				t.Fatal(err)
			}
			paths := []string{s.path("originals", journal.ID, ".original"), s.path("previews", journal.ID, ".gif")}
			for i := range min(step, 2) {
				// Do not write through hard links; emulate the actual replace rename.
				p, err := s.stage(output)
				if err != nil {
					t.Fatal(err)
				}
				if err = os.Rename(p, paths[i]); err != nil {
					t.Fatal(err)
				}
			}
			if step >= 3 {
				if _, err = s.db.Exec("UPDATE assets SET bytes=?,original_bytes=? WHERE id=?", len(output), len(output), journal.ID); err != nil {
					t.Fatal(err)
				}
			}
			if step == 4 {
				if err = os.Remove(filepath.Join(root, ".replacement")); err != nil {
					t.Fatal(err)
				}
			}
			if err = s.close(); err != nil {
				t.Fatal(err)
			}
			s, err = newStore(root)
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range paths {
				got, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(got, output) {
					t.Fatalf("restart inconsistent at step %d: %v", step, err)
				}
			}
			catalog, err := s.catalog()
			if err != nil || len(catalog.Assets) != 1 || catalog.Assets[0].Revision != journal.Revision || catalog.Assets[0].Bytes != journal.Bytes {
				t.Fatalf("restart metadata: %+v %v", catalog, err)
			}
			entries, err := os.ReadDir(filepath.Join(root, ".staging"))
			if err != nil || len(entries) != 0 {
				t.Fatalf("restart retained stages: %v", err)
			}
			if _, err = os.Stat(filepath.Join(root, ".replacement")); !os.IsNotExist(err) {
				t.Fatalf("restart retained journal: %v", err)
			}
		})
	}
}
