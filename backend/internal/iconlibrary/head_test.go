package iconlibrary

import (
	"fmt"
	"net/url"
	"testing"
)

func TestHeadReferencesUploadOnly(t *testing.T) {
	const upload = "upload-0123456789abcdef0123456789abcdef"
	config := fmt.Sprintf(`{"version":1,"selections":{"folder":"pack-folder","pdf":"%s","music":"legacy-smile","video":"default","other":"removed-asset"}}`, upload)
	head := `<meta id="openlist-icon-config" content="` + url.PathEscape(config) + `">`
	refs, err := HeadReferences(head)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || !refs[upload] {
		t.Fatalf("expected only uploaded reference, got %v", refs)
	}
}

func TestHeadReferencesRejectsAmbiguousConfiguration(t *testing.T) {
	for _, config := range []string{
		`{"version":1,"selections":{"folder":null}}`,
		`{"version":1,"selections":{"folder":123}}`,
		`{"version":1,"selections":{"folder":"default","folder":"upload-0123456789abcdef0123456789abcdef"}}`,
		`{"version":1,"selections":{},"selections":{}}`,
		`{"version":2,"selections":{}}`,
		`{"version":1,"selections":[]}`,
		`{"version":1,"selections":{}} {}`,
	} {
		t.Run(config, func(t *testing.T) {
			head := `<meta id="openlist-icon-config" content="` + url.PathEscape(config) + `">`
			if _, err := HeadReferences(head); err == nil {
				t.Fatal("ambiguous configuration allowed asset deletion")
			}
		})
	}
}

func TestHeadReferencesRejectsDuplicateMetadata(t *testing.T) {
	head := `<meta id="openlist-icon-config" content="` + url.PathEscape(`{"version":1,"selections":{}}`) + `">`
	if _, err := HeadReferences(head + head); err == nil {
		t.Fatal("duplicate configuration allowed asset deletion")
	}
}

func TestValidAssetUploadOnly(t *testing.T) {
	if !validAsset("upload-0123456789abcdef0123456789abcdef") {
		t.Fatal("valid upload rejected")
	}
	for _, id := range []string{"pack-folder", "pack-pdf", "legacy-smile", "default", "", "upload-0123456789abcdef0123456789abcde", "upload-0123456789abcdef0123456789abcdeg", "upload-0123456789ABCDEF0123456789ABCDEF"} {
		if validAsset(id) {
			t.Errorf("non-upload asset accepted: %q", id)
		}
	}
}
