package iconlibrary

import (
	"bytes"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"

	"golang.org/x/image/webp"
)

//go:embed seeds/*
var builtinFiles embed.FS

// Reserved import identity keeps native asset IDs valid without borrowing any
// deployed administrator's identity. Existing owner/request collisions fail closed.
const seedOwner = "18446744073709551615"

type seedEntry struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Format   string `json:"format"`
	Original string `json:"original"`
	Preview  string `json:"preview"`
}

func seedManifest(files fs.FS) ([]seedEntry, error) {
	data, err := fs.ReadFile(files, "seeds/manifest.json")
	if err != nil {
		return nil, err
	}
	var entries []seedEntry
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&entries); err != nil {
		return nil, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid builtin manifest trailer")
	}
	if len(entries) == 0 || len(entries) > MaxAssets {
		return nil, errors.New("invalid builtin manifest size")
	}
	seen := make(map[string]bool, len(entries))
	for _, entry := range entries {
		name, err := decodeName(url.PathEscape(entry.Name))
		if err != nil || name != entry.Name || entry.Key != contentRevision([]byte("boxlist-v1:" + entry.Name))[:32] || seen[entry.Key] || entry.Format != imageFormat(entry.Name) || entry.Original != entry.Key+"."+entry.Format || entry.Preview != entry.Key+previewSuffix(entry.Name) {
			return nil, errors.New("invalid builtin manifest entry")
		}
		seen[entry.Key] = true
	}
	return entries, nil
}

func seedBytes(files fs.FS, entry seedEntry) ([]byte, []byte, error) {
	original, err := fs.ReadFile(files, "seeds/"+entry.Original)
	if err != nil {
		return nil, nil, err
	}
	if len(original) == 0 || len(original) > MaxInputBytes {
		return nil, nil, errors.New("invalid builtin original size")
	}
	if entry.Format == "gif" {
		if _, err = validateGIF(original); err != nil {
			return nil, nil, err
		}
		return original, original, nil
	}
	if !bytes.HasPrefix(original, []byte("\x89PNG\r\n\x1a\n")) {
		return nil, nil, errors.New("invalid builtin PNG signature")
	}
	if _, err = decodePNG(original); err != nil {
		return nil, nil, err
	}
	preview, err := fs.ReadFile(files, "seeds/"+entry.Preview)
	if err != nil {
		return nil, nil, err
	}
	if len(preview) == 0 || len(preview) > MaxInputBytes {
		return nil, nil, errors.New("invalid builtin preview size")
	}
	config, err := webp.DecodeConfig(bytes.NewReader(preview))
	if err != nil || config.Width != 256 || config.Height != 256 {
		return nil, nil, errors.New("invalid builtin preview dimensions")
	}
	if _, err = webp.Decode(bytes.NewReader(preview)); err != nil {
		return nil, nil, err
	}
	return original, preview, nil
}

// installSeeds runs only before publishing a Service, after file recovery. Each
// seed has a durable pending reservation before commit and an installed marker
// afterward. Installed mappings are never removed or re-imported, even when the
// asset has a deletion tombstone or its bytes were edited by the user.
func (s *store) installSeeds(files fs.FS) error {
	entries, err := seedManifest(files)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err = s.installSeed(files, entry); err != nil {
			return fmt.Errorf("install builtin %s: %w", entry.Key, err)
		}
	}
	return nil
}

func (s *store) installSeed(files fs.FS, entry seedEntry) error {
	var id, state string
	err := s.db.QueryRow("SELECT asset_id,state FROM builtin_seeds WHERE seed_key=?", entry.Key).Scan(&id, &state)
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	reserved := err == nil
	if reserved && state == "installed" {
		var exists int
		if err = s.db.QueryRow("SELECT COUNT(*) FROM assets WHERE id=?", id).Scan(&exists); err != nil {
			return err
		}
		if exists != 1 || !validAsset(id) {
			return errors.New("installed builtin is missing its catalog record")
		}
		return nil
	}
	if reserved && (state != "pending" || id != uploadID(seedOwner, entry.Key)) {
		return errors.New("invalid builtin reservation")
	}
	original, preview, err := seedBytes(files, entry)
	if err != nil {
		return err
	}
	if !reserved {
		id, err = s.matchSeed(entry, preview)
		if err != nil {
			return err
		}
		if id != "" {
			_, err = s.db.Exec("INSERT INTO builtin_seeds(seed_key,asset_id,state) VALUES(?,?,'installed')", entry.Key, id)
			return err
		}
		id = uploadID(seedOwner, entry.Key)
		var collisions int
		if err = s.db.QueryRow("SELECT COUNT(*) FROM assets WHERE id=? OR (owner=? AND request_id=?)", id, seedOwner, entry.Key).Scan(&collisions); err != nil {
			return err
		}
		if collisions != 0 {
			return errors.New("builtin import identity collision")
		}
		if _, err = s.db.Exec("INSERT INTO builtin_seeds(seed_key,asset_id,state) VALUES(?,?,'pending')", entry.Key, id); err != nil {
			return err
		}
	}
	r, err := scanRecord(s.db.QueryRow("SELECT "+columns+" FROM assets WHERE id=?", id))
	if err == sql.ErrNoRows {
		if _, err = s.commit(seedOwner, entry.Key, entry.Name, original, preview); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else {
		if r.owner != seedOwner || r.request != entry.Key || r.Name != entry.Name || r.sha != contentRevision(original) {
			return errors.New("builtin reservation record mismatch")
		}
		// A tombstone wins even if interruption occurred before the installed
		// marker. Never retry commit in a way that could revive this record.
		if !r.deleted.Valid {
			if _, err = s.check(r, r.sha, r.Name); err != nil {
				return err
			}
		}
	}
	_, err = s.db.Exec("UPDATE builtin_seeds SET state='installed' WHERE seed_key=? AND asset_id=? AND state='pending'", entry.Key, id)
	return err
}

// Adopt the served content, not content_sha, which deliberately remains the
// original upload request hash after GIF replacement. Include tombstones so an
// already removed default is not resurrected on first adoption either.
func (s *store) matchSeed(entry seedEntry, preview []byte) (string, error) {
	rows, err := s.db.Query("SELECT "+columns+" FROM assets WHERE name=? AND bytes=? AND NOT EXISTS (SELECT 1 FROM builtin_seeds WHERE asset_id=assets.id) ORDER BY deleted_at IS NOT NULL,created_at,id", entry.Name, len(preview))
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return "", err
		}
		dir := "previews"
		if r.deleted.Valid {
			dir = "trash/previews"
		}
		data, err := readPrivateBytes(s.path(dir, r.ID, previewSuffix(r.Name)), r.Bytes)
		if err != nil {
			return "", err
		}
		if bytes.Equal(data, preview) {
			return r.ID, nil
		}
	}
	return "", rows.Err()
}
