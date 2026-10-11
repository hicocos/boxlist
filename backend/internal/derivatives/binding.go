package derivatives

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// Cloudreve V4 defines sub as the account identity on access and refresh JWTs.
// Reading it here is NOT signature verification/authorization: only configured
// drivers fetch the source, while HTTP grants still check local permissions.
// Missing/malformed/mismatched subjects keep opaque credentials in the binding.
func stableCloudreveCredentials(fields map[string]any) {
	subject := ""
	for _, key := range []string{"access_token", "refresh_token"} {
		token, _ := fields[key].(string)
		if token == "" {
			continue
		}
		parts := strings.Split(token, ".")
		if len(parts) != 3 || len(token) > 32768 {
			return
		}
		data, e := base64.RawURLEncoding.DecodeString(parts[1])
		if e != nil {
			return
		}
		var claims struct {
			Subject string `json:"sub"`
		}
		if json.Unmarshal(data, &claims) != nil || claims.Subject == "" || len(claims.Subject) > 1024 {
			return
		}
		if subject != "" && subject != claims.Subject {
			return
		}
		subject = claims.Subject
	}
	if subject == "" {
		return
	}
	delete(fields, "access_token")
	delete(fields, "refresh_token")
	fields["_derivative_cloudreve_subject"] = subject
}
