package handles

import (
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/OpenListTeam/go-cache"
	"time"
)

// User grants reuse the JWT authority's fingerprint state. Signing a derivative
// is read-only: an in-flight signer can never reinsert a logged-out session.
// Native API keys use their current DB value plus a TTL-scoped logout tombstone.
var derivativeNativeRevocations = cache.NewMemCache[bool]()

func derivativeSessionFingerprint(token string) string { return common.TokenFingerprint(token) }
func derivativeRememberSession(token string, expires time.Time) string {
	if !expires.After(time.Now()) {
		return ""
	}
	key := common.TokenFingerprint(token)
	if !common.TokenFingerprintValid(key) {
		return ""
	}
	return key
}
func derivativeRevokeSession(token string) {
	_ = common.InvalidateToken(token)
	derivativeNativeRevocations.Set(common.TokenFingerprint(token), true, cache.WithEx[bool](derivativeTicketTTL+time.Minute))
}
func derivativeSessionValid(key string) bool { return key != "" && common.TokenFingerprintValid(key) }
func derivativeNativeSessionValid(key string) bool {
	_, revoked := derivativeNativeRevocations.Get(key)
	return key != "" && !revoked
}
