package derivatives

import (
	"encoding/base64"
	"encoding/json"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"testing"
)

func bindingJWT(subject string, expires int) string {
	b, _ := json.Marshal(map[string]any{"sub": subject, "exp": expires})
	return "test." + base64.RawURLEncoding.EncodeToString(b) + ".not-a-real-signature"
}
func TestCloudreveRotationRetainsIdentity(t *testing.T) {
	fields := map[string]any{"address": "https://source.invalid", "root_folder_path": "cloudreve://my", "username": "account", "access_token": bindingJWT("account-id", 1), "refresh_token": bindingJWT("account-id", 2)}
	storage := func() model.Storage {
		b, _ := json.Marshal(fields)
		return model.Storage{ID: 5, Driver: "Cloudreve V4", MountPath: "/cloud", Addition: string(b)}
	}
	s := storage()
	before := Binding(&s)
	fields["access_token"] = bindingJWT("account-id", 10)
	fields["refresh_token"] = bindingJWT("account-id", 20)
	s = storage()
	if before != Binding(&s) {
		t.Fatal("automatic token rotation changed source identity")
	}
	fields["access_token"] = bindingJWT("other-account", 30)
	fields["refresh_token"] = bindingJWT("other-account", 40)
	s = storage()
	if before == Binding(&s) {
		t.Fatal("account replacement kept binding")
	}
	fields["access_token"] = bindingJWT("account-id", 10)
	fields["refresh_token"] = bindingJWT("account-id", 20)
	for _, key := range []string{"address", "root_folder_path", "username", "password"} {
		old := fields[key]
		fields[key] = "changed"
		s = storage()
		if before == Binding(&s) {
			t.Fatal("identity setting ignored", key)
		}
		fields[key] = old
		if old == nil {
			delete(fields, key)
		}
	}
	fields["access_token"] = "malformed-A"
	s = storage()
	opaque := Binding(&s)
	fields["access_token"] = "malformed-B"
	s = storage()
	if opaque == Binding(&s) {
		t.Fatal("unknown credential silently ignored")
	}
}
