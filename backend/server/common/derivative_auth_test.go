package common

import (
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"testing"
)

func TestTokenFingerprintRevocationAndUniqueSessions(t *testing.T) {
	old := conf.Conf
	conf.Conf = &conf.Config{TokenExpiresIn: 1}
	t.Cleanup(func() { conf.Conf = old })
	u := &model.User{ID: 1, Username: "token-test", PwdTS: 1}
	first, e := GenerateToken(u)
	if e != nil {
		t.Fatal(e)
	}
	second, e := GenerateToken(u)
	if e != nil || first == second {
		t.Fatal("same-second logins collided", e)
	}
	key := TokenFingerprint(first)
	if !TokenFingerprintValid(key) {
		t.Fatal("fingerprint missing")
	}
	if e = InvalidateToken(first); e != nil {
		t.Fatal(e)
	}
	if TokenFingerprintValid(key) {
		t.Fatal("revocation missed derivative identity")
	}
	if _, e = ParseToken(first); e == nil {
		t.Fatal("revoked JWT accepted")
	}
	if !TokenFingerprintValid(TokenFingerprint(second)) {
		t.Fatal("one logout revoked other session")
	}
	_ = InvalidateToken(second)
}
