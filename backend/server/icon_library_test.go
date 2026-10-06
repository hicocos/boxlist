package server

import (
	"net/http/httptest"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
)

func TestIconOriginBoundaries(t *testing.T) {
	old := conf.Conf
	conf.Conf = &conf.Config{}
	t.Cleanup(func() { conf.Conf = old })
	cases := []struct {
		site, host, origin string
		want               bool
	}{
		{"https://pan.example", "pan.example", "https://pan.example", true},
		{"https://pan.example", "internal:5244", "https://pan.example", true},
		{"https://pan.example", "pan.example", "https://evil.example", false},
		{"https://pan.example", "pan.example", "http://pan.example", false},
		{"https://pan.example", "pan.example", "https://pan.example/", false},
		{"https://pan.example", "pan.example", "", false},
		{"https://pan.example/pan", "internal", "https://pan.example", true},
		{"", "pan.example:8443", "https://pan.example:8443", true},
		{"", "internal:5244", "https://pan.example", false},
		{"", "pan.example", "https://evil.example", false},
		{"https://pan.example", "pan.example", "https://u:p@pan.example", false},
	}
	for _, tc := range cases {
		conf.Conf.SiteURL = tc.site
		r := httptest.NewRequest("POST", "/icon/api/upload", nil)
		r.Host = tc.host
		r.Header.Set("Origin", tc.origin)
		r.Header.Set("X-Forwarded-Host", "pan.example")
		if got := checkIconOrigin(r); got != tc.want {
			t.Errorf("site=%q host=%q origin=%q got=%v", tc.site, tc.host, tc.origin, got)
		}
	}
	conf.Conf.SiteURL = "https://pan.example"
	r := httptest.NewRequest("POST", "/icon/api/upload", nil)
	r.Header.Add("Origin", "https://pan.example")
	r.Header.Add("Origin", "https://pan.example")
	if checkIconOrigin(r) {
		t.Fatal("duplicate Origin accepted")
	}
}

func TestIconMissingAuthDoesNotReadDatabase(t *testing.T) {
	for _, token := range []string{"", " ", " leading", "trailing "} {
		r := httptest.NewRequest("POST", "/icon/api/upload", nil)
		r.Header.Set("Authorization", token)
		_, err := authorizeIconRequest(r)
		if err == nil {
			t.Fatal("missing/malformed Authorization accepted")
		}
	}
}
