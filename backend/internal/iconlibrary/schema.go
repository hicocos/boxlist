package iconlibrary

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
)

// Validate recognized Python-compatible schema BEFORE recovery can unlink files.
func (s *store) validateSchema() error {
	for table, want := range map[string]map[string]string{
		"assets":          {"id": "TEXT", "owner": "TEXT", "request_id": "TEXT", "content_sha": "TEXT", "name": "TEXT", "width": "INTEGER", "height": "INTEGER", "bytes": "INTEGER", "original_bytes": "INTEGER", "created_at": "TEXT", "deleted_at": "TEXT"},
		"hidden_builtins": {"id": "TEXT", "deleted_at": "TEXT"},
	} {
		rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		for rows.Next() {
			var cid, nn, pk int
			var name, typ string
			var def any
			if err = rows.Scan(&cid, &name, &typ, &nn, &def, &pk); err != nil {
				rows.Close()
				return err
			}
			expected, ok := want[name]
			if !ok || strings.ToUpper(typ) != expected || (name == "id" && pk != 1) {
				rows.Close()
				return errors.New("unrecognized catalog schema")
			}
			seen[name] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(seen) != len(want) {
			return errors.New("incomplete catalog schema")
		}
	}
	var triggers int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND tbl_name IN ('assets','hidden_builtins')").Scan(&triggers); err != nil {
		return err
	}
	if triggers != 0 {
		return errors.New("unexpected catalog triggers")
	}
	// Verify the owner/request uniqueness constraint (not merely matching columns).
	rows, err := s.db.Query("PRAGMA index_list(assets)")
	if err != nil {
		return err
	}
	indexes := []string{}
	for rows.Next() {
		var seq, unique, partial int
		var name, origin string
		if err = rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
			rows.Close()
			return err
		}
		if unique == 1 && partial == 0 {
			indexes = append(indexes, name)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	found := false
	for _, name := range indexes {
		r, e := s.db.Query("PRAGMA index_info('" + strings.ReplaceAll(name, "'", "''") + "')")
		if e != nil {
			return e
		}
		cols := []string{}
		for r.Next() {
			var seq, cid int
			var col string
			if e = r.Scan(&seq, &cid, &col); e != nil {
				r.Close()
				return e
			}
			cols = append(cols, col)
		}
		e = r.Err()
		r.Close()
		if e != nil {
			return e
		}
		if len(cols) == 2 && cols[0] == "owner" && cols[1] == "request_id" {
			found = true
		}
	}
	if !found {
		return errors.New("missing request uniqueness constraint")
	}
	var count, total int64
	if err = s.db.QueryRow("SELECT COUNT(*),COALESCE(SUM(bytes+original_bytes),0) FROM assets").Scan(&count, &total); err != nil {
		return err
	}
	if count > MaxAssets || total > MaxStoredBytes {
		return errors.New("catalog exceeds capacity")
	}
	_, err = s.catalog()
	return err
}
func uploadID(owner, key string) string {
	return "upload-" + fmt.Sprintf("%x", sha256.Sum256([]byte(owner+":"+key)))[:32]
}

func validRecord(r record) bool {
	if !assetPattern.MatchString(r.ID) || !identityPattern.MatchString(r.owner) || !requestPattern.MatchString(r.request) || len(r.sha) != 64 || r.Width != 256 || r.Height != 256 || r.Bytes <= 0 || r.Bytes > MaxInputBytes || r.originalBytes <= 0 || r.originalBytes > MaxInputBytes {
		return false
	}
	if r.ID != "upload-"+fmt.Sprintf("%x", sha256.Sum256([]byte(r.owner+":"+r.request)))[:32] {
		return false
	}
	for _, c := range r.sha {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
