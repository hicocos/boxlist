package derivatives

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestR2ProviderValidationAndLegacyIdentity(t *testing.T) {
	r := S3ConfigRequest{Provider: "r2", Endpoint: "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com", Bucket: "previews", Prefix: "图片/"}
	target, err := normalizeS3Target(r)
	if err != nil || target.Region != "auto" || !target.PathStyle {
		t.Fatalf("R2 normalization: %v", err)
	}
	for _, endpoint := range []string{"http://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com", "https://cdn.example.com", "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com.evil.example", "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com:443"} {
		invalid := r
		invalid.Endpoint = endpoint
		if _, err := normalizeS3Target(invalid); err == nil {
			t.Fatalf("accepted endpoint %s", endpoint)
		}
	}
	generic := *target
	generic.Provider = "s3"
	if s3Identity(target) == s3Identity(&generic) {
		t.Fatal("R2 shares S3 target identity")
	}
	legacy := generic
	legacy.Provider = ""
	if s3Identity(&legacy) != s3Identity(&generic) {
		t.Fatal("legacy credential binding changed")
	}
	// Old backup checksums depend on absent fields remaining absent in JSON.
	data, err := json.Marshal(S3ConfigRequest{})
	if err != nil || strings.Contains(string(data), "provider") {
		t.Fatal("legacy backup JSON changed", err)
	}
}

func TestR2SignedArtifactDelivery(t *testing.T) {
	m := coreManager(t)
	target, err := normalizeS3Target(S3ConfigRequest{Provider: "r2", Endpoint: "https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com", Bucket: "previews", Prefix: "图片/"})
	if err != nil {
		t.Fatal(err)
	}
	target.IdentityHash = s3Identity(target)
	key, err := m.s3EncryptionKey(true)
	if err != nil {
		t.Fatal(err)
	}
	target.Credentials, err = sealS3Credentials(key, target.IdentityHash, "test-access", "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.db.Create(target).Error; err != nil {
		t.Fatal(err)
	}
	id := digest("r2-artifact")
	objectKey, err := s3ArtifactKey(target, id)
	if err != nil {
		t.Fatal(err)
	}
	a := Artifact{ID: id, Profile: "thumb", OutputTargetID: target.ID, ObjectKey: objectKey, Bytes: 12, SHA256: digest("body")}
	signatures := map[string]string{}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		address, err := m.SignedArtifactURL(context.Background(), a, method)
		if err != nil {
			t.Fatal(err)
		}
		u, err := url.Parse(address)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		if u.Host != "0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com" || u.Path != "/previews/"+objectKey || q.Get("X-Amz-Expires") != "300" || q.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" || !strings.Contains(q.Get("X-Amz-Credential"), "/auto/s3/aws4_request") {
			t.Fatal("incorrect signed request scope")
		}
		if len(q.Get("X-Amz-Signature")) != 64 {
			t.Fatal("missing signature")
		}
		if method == http.MethodGet && q.Get("response-cache-control") != "private, no-store" {
			t.Fatal("GET can cache private preview")
		}
		signatures[method] = q.Get("X-Amz-Signature")
	}
	if signatures[http.MethodGet] == signatures[http.MethodHead] {
		t.Fatal("HEAD reused GET signature")
	}
	invalid := a
	invalid.ObjectKey = "other/file.webp"
	if _, err := m.SignedArtifactURL(context.Background(), invalid, http.MethodGet); err == nil {
		t.Fatal("signed foreign object key")
	}
	if _, err := m.SignedArtifactURL(context.Background(), a, http.MethodDelete); err == nil {
		t.Fatal("signed unsupported method")
	}
	if err := m.db.Model(target).Updates(map[string]any{"public_cdn": true, "cdn_url": "https://images.example.com"}).Error; err != nil {
		t.Fatal(err)
	}
	if address, err := m.SignedArtifactURL(context.Background(), a, http.MethodGet); err != nil || address != "" {
		t.Fatal("public mode issued private signature", err)
	}
	public := m.PublicArtifactURL(a)
	u, err := url.Parse(public)
	if err != nil || u.Host != "images.example.com" || u.Path != "/"+objectKey || u.RawQuery != "" {
		t.Fatal("invalid public object URL")
	}
	backup, err := m.BackupS3Previews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := validatePreviewBackup(backup)
	if err != nil || restored[target.ID].Provider != "r2" {
		t.Fatal("backup lost R2 delivery", err)
	}
}
