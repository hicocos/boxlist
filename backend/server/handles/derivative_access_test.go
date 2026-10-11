package handles

import (
	"strings"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/server/common"
)

func TestDerivativeSessionRevocation(t *testing.T) {
	old := conf.Conf
	conf.Conf = &conf.Config{TokenExpiresIn: 1}
	t.Cleanup(func() { conf.Conf = old })
	token, e := common.GenerateToken(&model.User{ID: 1, Username: "session", PwdTS: 1})
	if e != nil {
		t.Fatal(e)
	}
	id := derivativeRememberSession(token, time.Now().Add(time.Minute))
	if id == "" || !derivativeSessionValid(id) {
		t.Fatal("session missing")
	}
	if strings.Contains(id, "unit-session") {
		t.Fatal("token leaked")
	}
	derivativeRevokeSession(token)
	if derivativeRememberSession(token, time.Now().Add(time.Minute)) != "" {
		t.Fatal("in-flight signer resurrected session")
	}
	if derivativeSessionValid(id) {
		t.Fatal("logout failed to revoke session")
	}
}

func TestDerivativeTicketTamperExpiryAndPurpose(t *testing.T) {
	old := conf.Conf
	conf.Conf = &conf.Config{JwtSecret: "test-only-do-not-deploy"}
	t.Cleanup(func() { conf.Conf = old })
	original := derivativeTicket{ID: strings.Repeat("a", 64), Profile: "thumb", User: 7, Grant: "grant", Expires: time.Now().Add(time.Minute).Unix()}
	token := signDerivativeTicket(original)
	parsed, err := parseDerivativeTicket(token)
	if err != nil || parsed != original {
		t.Fatalf("round trip: %v", err)
	}
	for _, bad := range []string{"", token + "x", strings.Replace(token, ".", "x.", 1), strings.Repeat("a", 3000)} {
		if _, err := parseDerivativeTicket(bad); err == nil {
			t.Error("accepted invalid ticket")
		}
	}
	for _, expiry := range []int64{time.Now().Add(-time.Minute).Unix(), time.Now().Add(time.Hour).Unix()} {
		v := original
		v.Expires = expiry
		if _, err := parseDerivativeTicket(signDerivativeTicket(v)); err == nil {
			t.Error("accepted invalid expiry")
		}
	}
	v := original
	v.Profile = "../../reader"
	if _, err := parseDerivativeTicket(signDerivativeTicket(v)); err == nil {
		t.Error("accepted unknown profile")
	}
}

func TestDerivativeScopePreservesCaseAndRejectsTraversal(t *testing.T) {
	for _, p := range []string{"/books", "/books/a.png", "/漫画/é.png"} {
		if !validDerivativePath(p) {
			t.Error(p)
		}
	}
	for _, p := range []string{"../x", "/books/../secret", "/a//b", "/a\\b", "/./a", "/a\x00b"} {
		if validDerivativePath(p) {
			t.Errorf("accepted %q", p)
		}
	}
	if derivativeWithin("/books", "/books-other/a") || derivativeWithin("/books", "/Books/a") {
		t.Error("scope escaped")
	}
	if !derivativeWithin("/books", "/books/a") {
		t.Error("valid scope rejected")
	}
}

func TestDerivativeGrantRevokesWithLocalACLChanges(t *testing.T) {
	old := conf.Conf
	conf.Conf = &conf.Config{JwtSecret: "test-only"}
	t.Cleanup(func() { conf.Conf = old })
	user := &model.User{ID: 7, BasePath: "/books", Permission: 0, Role: model.GENERAL, PwdTS: 1, PwdHash: "hash"}
	meta := &model.Meta{ID: 1, Path: "/books", Password: "private-password", PSub: true}
	name := "/books/a.png"
	before := derivativeGrant(user, meta, nil, name)
	if strings.Contains(before, "private-password") {
		t.Error("password leaked")
	}
	if derivativeAccessAllowed(user, meta, nil, name, "wrong", false) {
		t.Error("wrong password allowed")
	}
	if !derivativeAccessAllowed(user, meta, nil, name, "private-password", false) {
		t.Error("correct password denied")
	}
	if !derivativeAccessAllowed(user, meta, nil, name, "", true) {
		t.Error("valid grant denied")
	}
	for _, change := range []func(){func() { meta.Password = "rotated" }, func() { meta.ReadUsers = []uint{8}; meta.ReadUsersSub = true }, func() { user.BasePath = "/other" }, func() { user.Permission = 2 }, func() { user.PwdTS++ }} {
		change()
		after := derivativeGrant(user, meta, nil, name)
		if before == after {
			t.Error("grant survived authorization change")
		}
		before = after
	}
	if derivativeAccessAllowed(user, meta, nil, name, "", true) {
		t.Error("base path revocation bypassed")
	}
	user.BasePath = "/books"
	if derivativeAccessAllowed(user, meta, nil, name, "", true) {
		t.Error("read ACL bypassed")
	}
	user.Disabled = true
	if derivativeAccessAllowed(user, nil, nil, name, "", true) {
		t.Error("disabled user allowed")
	}
}

func TestDerivativeNearestMetaAndShareScope(t *testing.T) {
	metas := []model.Meta{{ID: 1, Path: "/", Password: "root"}, {ID: 2, Path: "/Books", Password: "local"}}
	m, err := derivativeNearestMeta(metas, "/books/one/a.png")
	if err != nil || m.ID != 2 {
		t.Fatalf("nearest meta: %v %v", m, err)
	}
	metas = append(metas, model.Meta{ID: 3, Path: "/books"})
	if _, err := derivativeNearestMeta(metas, "/books/a.png"); err == nil {
		t.Error("ambiguous metadata accepted")
	}
	u := &model.User{ID: 8, BasePath: "/books", Permission: 1 << 14}
	s := &model.Sharing{SharingDB: &model.SharingDB{ID: "share", CreatorId: 8}, Files: []string{"/books/chapter"}, Creator: u}
	if !derivativeAccessAllowed(u, nil, s, "/books/chapter/1.jpg", "", false) {
		t.Error("share path denied")
	}
	if derivativeAccessAllowed(u, nil, s, "/books/chapter-other/1.jpg", "", false) {
		t.Error("share escape allowed")
	}
	s.Disabled = true
	if derivativeAccessAllowed(u, nil, s, "/books/chapter/1.jpg", "", true) {
		t.Error("revoked share allowed")
	}
	s.Disabled = false
	expiry := time.Now().Add(-time.Second)
	s.Expires = &expiry
	if derivativeAccessAllowed(u, nil, s, "/books/chapter/1.jpg", "", true) {
		t.Error("expired share allowed")
	}
}
