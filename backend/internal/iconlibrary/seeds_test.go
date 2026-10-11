package iconlibrary

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"testing"
	"testing/fstest"
)

func seedFixture(t *testing.T) (fstest.MapFS, []seedEntry) {
	t.Helper()
	files := fstest.MapFS{}
	entries := []seedEntry{}
	for _, name := range []string{"内置.png", "内置.gif"} {
		key := contentRevision([]byte("boxlist-v1:" + name))[:32]
		entry := seedEntry{Key: key, Name: name, Format: imageFormat(name), Original: key + "." + imageFormat(name), Preview: key + previewSuffix(name)}
		var original []byte
		if entry.Format == "png" {
			original = pngFixture(t, 2, 2)
		} else {
			original = gifFixture(t, 2)
		}
		preview, err := ConvertImage(name, original)
		if err != nil {
			t.Fatal(err)
		}
		files["seeds/"+entry.Original] = &fstest.MapFile{Data: original}
		files["seeds/"+entry.Preview] = &fstest.MapFile{Data: preview}
		entries = append(entries, entry)
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	files["seeds/manifest.json"] = &fstest.MapFile{Data: data}
	return files, entries
}

func TestEmbeddedSeedsFreshService(t *testing.T) {
	s, err := New(t.TempDir(), transparencyHooks())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	entries, err := seedManifest(builtinFiles)
	if err != nil || len(entries) != 34 {
		t.Fatalf("release manifest: %d, %v", len(entries), err)
	}
	catalog, err := s.store.catalog()
	if err != nil || len(catalog.Assets) != len(entries) {
		t.Fatalf("fresh defaults: %+v, %v", catalog, err)
	}
	formats := map[string]int{}
	for _, asset := range catalog.Assets {
		if asset.Source != "builtin" || !validRevision(asset.Revision) {
			t.Fatalf("invalid seeded asset: %+v", asset)
		}
		formats[asset.Format]++
	}
	if formats["png"] != 25 || formats["gif"] != 9 {
		t.Fatalf("release formats: %v", formats)
	}
}

func TestSeedsRestartDeletionAndUpdatedManifest(t *testing.T) {
	files, entries := seedFixture(t)
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
	if err = s.installSeeds(files); err != nil {
		t.Fatal(err)
	}
	before, err := s.catalog()
	if err != nil || len(before.Assets) != 2 {
		t.Fatalf("fresh fixture: %+v, %v", before, err)
	}
	for _, entry := range entries {
		id := uploadID(seedOwner, entry.Key)
		got, suffix, err := s.preview(id)
		if err != nil || suffix != previewSuffix(entry.Name) || !bytes.Equal(got, files["seeds/"+entry.Preview].Data) {
			t.Fatalf("seed bytes changed: %s, %v", entry.Name, err)
		}
	}
	if err = s.close(); err != nil {
		t.Fatal(err)
	}
	s, err = newStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.installSeeds(files); err != nil {
		t.Fatal(err)
	}
	after, err := s.catalog()
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("restart changed seeds: %+v, %v", after, err)
	}
	for _, asset := range after.Assets {
		if err = s.delete(asset.ID); err != nil {
			t.Fatal(err)
		}
	}
	// Existing seed keys remain installed even if a later release changes bytes.
	for _, entry := range entries {
		files["seeds/"+entry.Original].Data = []byte("changed release bytes")
	}
	for range 2 {
		if err = s.close(); err != nil {
			t.Fatal(err)
		}
		s, err = newStore(root)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.installSeeds(files); err != nil {
			t.Fatal(err)
		}
		catalog, err := s.catalog()
		if err != nil || len(catalog.Assets) != 0 {
			t.Fatalf("deleted defaults resurrected: %+v, %v", catalog, err)
		}
		for _, asset := range before.Assets {
			if _, _, err = s.preview(asset.ID); err == nil {
				t.Fatal("deleted preview is public")
			}
		}
	}
}

func TestSeedsAdoptLiveIDsAndEditedGIF(t *testing.T) {
	files, entries := seedFixture(t)
	s, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	want := map[string]Asset{}
	for _, entry := range entries {
		original := files["seeds/"+entry.Original].Data
		preview := files["seeds/"+entry.Preview].Data
		if entry.Format == "gif" {
			original = gifFixture(t, 1)
			preview = original
		}
		result, err := s.commit("1", entry.Key, entry.Name, original, preview)
		if err != nil {
			t.Fatal(err)
		}
		if result.Asset.Source != "uploaded" {
			t.Fatal("fresh upload is not uploaded")
		}
		asset := result.Asset
		if entry.Format == "gif" {
			asset, err = s.replaceGIF(asset.ID, asset.Revision, files["seeds/"+entry.Preview].Data, func() error { return nil })
			if err != nil {
				t.Fatal(err)
			}
		}
		asset.Source = "builtin"
		want[asset.ID] = asset
	}
	if err = s.installSeeds(files); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.catalog()
	if err != nil || len(catalog.Assets) != 2 {
		t.Fatalf("adoption duplicated: %+v, %v", catalog, err)
	}
	for _, asset := range catalog.Assets {
		if asset != want[asset.ID] {
			t.Fatalf("adoption changed live metadata: %+v", asset)
		}
		if asset.Format == "gif" {
			input, _, err := s.transparencyInput(asset.ID, asset.Revision)
			if err != nil || input.Source != "builtin" {
				t.Fatalf("transparency source: %+v, %v", input, err)
			}
			replaced, err := s.replaceGIF(asset.ID, asset.Revision, gifFixture(t, 0), func() error { return nil })
			if err != nil || replaced.Source != "builtin" {
				t.Fatalf("replacement source: %+v, %v", replaced, err)
			}
			retry, exists, err := s.retry("1", entries[1].Key, entries[1].Name, gifFixture(t, 1))
			if err != nil || !exists || retry.Asset.Source != "builtin" || retry.Asset != replaced {
				t.Fatalf("retry loses source or edit: %+v, %v", retry, err)
			}
		}
	}
	if err = s.installSeeds(files); err != nil {
		t.Fatal(err)
	}
}

func TestSeedsAdoptExistingDeletion(t *testing.T) {
	files, entries := seedFixture(t)
	s, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for _, entry := range entries {
		asset, err := s.commit("1", entry.Key, entry.Name, files["seeds/"+entry.Original].Data, files["seeds/"+entry.Preview].Data)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.delete(asset.Asset.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.installSeeds(files); err != nil {
		t.Fatal(err)
	}
	catalog, err := s.catalog()
	if err != nil || len(catalog.Assets) != 0 {
		t.Fatalf("pre-adoption deletion resurrected: %+v, %v", catalog, err)
	}
}

func TestSeedsPartialRecovery(t *testing.T) {
	for _, boundary := range []string{"reserved", "orphan-original", "orphan-preview", "committed", "deleted"} {
		t.Run(boundary, func(t *testing.T) {
			files, entries := seedFixture(t)
			entry := entries[0]
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
			id := uploadID(seedOwner, entry.Key)
			if _, err = s.db.Exec("INSERT INTO builtin_seeds VALUES(?,?,'pending')", entry.Key, id); err != nil {
				t.Fatal(err)
			}
			if boundary == "orphan-original" || boundary == "orphan-preview" {
				if err = os.WriteFile(s.path("originals", id, ".original"), files["seeds/"+entry.Original].Data, 0600); err != nil {
					t.Fatal(err)
				}
				if boundary == "orphan-preview" {
					if err = os.WriteFile(s.path("previews", id, ".webp"), files["seeds/"+entry.Preview].Data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			if boundary == "committed" || boundary == "deleted" {
				result, err := s.commit(seedOwner, entry.Key, entry.Name, files["seeds/"+entry.Original].Data, files["seeds/"+entry.Preview].Data)
				if err != nil || result.Asset.Source != "builtin" {
					t.Fatalf("seed commit source: %+v, %v", result, err)
				}
				if boundary == "deleted" {
					if err = s.delete(id); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err = s.close(); err != nil {
				t.Fatal(err)
			}
			s, err = newStore(root)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.installSeeds(files); err != nil {
				t.Fatal(err)
			}
			catalog, err := s.catalog()
			want := 2
			if boundary == "deleted" {
				want = 1
			}
			if err != nil || len(catalog.Assets) != want {
				t.Fatalf("partial recovery: %+v, %v", catalog, err)
			}
			var pending int
			if err = s.db.QueryRow("SELECT COUNT(*) FROM builtin_seeds WHERE state='pending'").Scan(&pending); err != nil || pending != 0 {
				t.Fatalf("pending marker: %d, %v", pending, err)
			}
		})
	}
}

func TestSeedsInvalidBytesAndCollisionPreserveUploads(t *testing.T) {
	for _, problem := range []string{"png", "webp", "gif", "collision"} {
		t.Run(problem, func(t *testing.T) {
			files, entries := seedFixture(t)
			s, err := newStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			entry := entries[0]
			owner, key := "1", entry.Key
			if problem == "collision" {
				owner = seedOwner
			}
			asset, err := s.commit(owner, key, "user.png", []byte("user original"), []byte("user preview"))
			if err != nil {
				t.Fatal(err)
			}
			switch problem {
			case "png":
				files["seeds/"+entry.Original].Data = []byte("invalid PNG")
			case "webp":
				files["seeds/"+entry.Preview].Data = []byte("invalid WebP")
			case "gif":
				files["seeds/"+entries[1].Original].Data = []byte("GIF89a")
			}
			if err = s.installSeeds(files); err == nil {
				t.Fatal("invalid seed import succeeded")
			}
			data, _, err := s.preview(asset.Asset.ID)
			if err != nil || string(data) != "user preview" {
				t.Fatalf("failed import lost user data: %q, %v", data, err)
			}
			if problem != "collision" {
				files, _ = seedFixture(t)
				if err = s.installSeeds(files); err != nil {
					t.Fatalf("repair retry: %v", err)
				}
			}
		})
	}
}

func TestBuiltinDeletionUsesReferenceGuard(t *testing.T) {
	var head string
	hooks := transparencyHooks()
	hooks.ReadHead = func(*http.Request) (string, error) { return head, nil }
	s, err := New(t.TempDir(), hooks)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	catalog, err := s.store.catalog()
	if err != nil || len(catalog.Assets) == 0 {
		t.Fatalf("missing defaults: %v", err)
	}
	asset := catalog.Assets[0]
	remove := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodDelete, "/icon/api/assets/"+asset.ID, nil)
		r.Header.Set("Authorization", "test-token")
		r.Header.Set("Origin", "https://example.test")
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		return w
	}
	head = `<meta id="openlist-icon-config" content="` + url.PathEscape(`{"version":1,"selections":{"folder":"`+asset.ID+`"}}`) + `">`
	if w := remove(); w.Code != http.StatusConflict {
		t.Fatalf("referenced builtin not protected: %d %s", w.Code, w.Body.String())
	}
	head = ""
	w := remove()
	if w.Code != http.StatusOK || !bytes.Contains(w.Body.Bytes(), []byte(`"kind":"builtin"`)) {
		t.Fatalf("builtin delete failed: %d %s", w.Code, w.Body.String())
	}
	if err = s.store.installSeeds(builtinFiles); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.store.preview(asset.ID); err == nil {
		t.Fatal("deleted builtin reappeared")
	}
}

func TestSeedsRejectInvalidManifest(t *testing.T) {
	for _, problem := range []string{"duplicate", "key", "format", "path", "name", "trailing"} {
		t.Run(problem, func(t *testing.T) {
			files, entries := seedFixture(t)
			switch problem {
			case "duplicate":
				entries = append(entries, entries[0])
			case "key":
				entries[0].Key = "00000000000000000000000000000000"
			case "format":
				entries[0].Format = "gif"
			case "path":
				entries[0].Original = "../private.png"
			case "name":
				entries[0].Name = "../private.png"
			}
			data, err := json.Marshal(entries)
			if err != nil {
				t.Fatal(err)
			}
			if problem == "trailing" {
				data = append(data, []byte(` {}`)...)
			}
			files["seeds/manifest.json"].Data = data
			s, err := newStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer s.close()
			if err = s.installSeeds(files); err == nil {
				t.Fatal("invalid manifest accepted")
			}
			catalog, err := s.catalog()
			if err != nil || len(catalog.Assets) != 0 {
				t.Fatalf("invalid manifest changed catalog: %+v, %v", catalog, err)
			}
		})
	}
}

func TestInstalledSeedMissingRecordFailsClosed(t *testing.T) {
	files, entries := seedFixture(t)
	s, err := newStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.close()
	entry := entries[0]
	if _, err = s.db.Exec("INSERT INTO builtin_seeds VALUES(?,?,'installed')", entry.Key, uploadID(seedOwner, entry.Key)); err != nil {
		t.Fatal(err)
	}
	if err = s.installSeeds(files); err == nil {
		t.Fatal("missing installed asset was silently recreated")
	}
	var count int
	if err = s.db.QueryRow("SELECT COUNT(*) FROM assets").Scan(&count); err != nil || count != 0 {
		t.Fatalf("missing installed asset resurrected: %d, %v", count, err)
	}
}
