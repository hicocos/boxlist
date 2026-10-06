package server

// Custom addition: native icon HTTP routes. Keep this adapter small on upstream upgrades.
import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/OpenListTeam/OpenList/v4/cmd/flags"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/iconlibrary"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

func authorizeIconRequest(r *http.Request) (string, error) {
	token := r.Header.Get("Authorization")
	if token == "" || strings.TrimSpace(token) != token || len(token) > 4096 {
		return "", iconlibrary.Fault{Status: 401, Message: "请先登录管理员账号"}
	}
	var user *model.User
	var err error
	// Read the real database, not cached user/setting state, so revocation is rechecked.
	nativeToken, tokenErr := db.GetSettingItemByKey(conf.Token)
	if tokenErr == nil && nativeToken.Value != "" && subtle.ConstantTimeCompare([]byte(token), []byte(nativeToken.Value)) == 1 {
		user, err = db.GetUserByRole(model.ADMIN)
	} else {
		claims, parseErr := common.ParseToken(token)
		if parseErr != nil {
			return "", iconlibrary.Fault{Status: 401, Message: "登录无效或权限不足"}
		}
		user, err = db.GetUserByName(claims.Username)
		if err == nil && user.PwdTS != claims.PwdTS {
			return "", iconlibrary.Fault{Status: 401, Message: "登录状态已变化"}
		}
	}
	if err != nil || user == nil || user.ID == 0 {
		return "", iconlibrary.Fault{Status: 401, Message: "登录无效或权限不足"}
	}
	if user.Disabled || !user.IsAdmin() {
		return "", iconlibrary.Fault{Status: 403, Message: "仅启用的管理员可管理素材"}
	}
	return strconv.FormatUint(uint64(user.ID), 10), nil
}

func readIconHead(r *http.Request) (string, error) {
	if _, err := authorizeIconRequest(r); err != nil {
		return "", err
	}
	item, err := db.GetSettingItemByKey(conf.CustomizeHead)
	if err != nil || item == nil {
		return "", iconlibrary.Fault{Status: 503, Message: "无法核对图标配置，未删除"}
	}
	return item.Value, nil
}

func checkIconOrigin(r *http.Request) bool {
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return false
	}
	origin := values[0]
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	if conf.Conf.SiteURL != "" {
		configured, err := url.Parse(conf.Conf.SiteURL)
		return err == nil && configured.Scheme == "https" && configured.User == nil && origin == configured.Scheme+"://"+configured.Host
	}
	// Unconfigured installs allow only exact request host, never trust X-Forwarded-Host.
	// A deployment with a reverse proxy should set SITE_URL to its canonical HTTPS site.
	return origin == "https://"+r.Host
}

func initIconLibrary(g *gin.RouterGroup) {
	root, err := filepath.Abs(filepath.Join(flags.DataDir, "icon"))
	var handler http.Handler
	if err == nil {
		handler, err = iconlibrary.New(root, iconlibrary.Hooks{
			Authorize: authorizeIconRequest, ReadHead: readIconHead, CheckOrigin: checkIconOrigin,
		})
	}
	if err != nil {
		log.Error("native icon library unavailable; check private data directory")
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"code":503,"message":"图标服务初始化失败，请检查数据目录"}`))
		})
	}
	base := strings.TrimSuffix(g.BasePath(), "/")
	serve := func(c *gin.Context) {
		request := c.Request.Clone(c.Request.Context())
		copiedURL := *request.URL
		copiedURL.Path = strings.TrimPrefix(copiedURL.Path, base)
		if copiedURL.RawPath != "" {
			copiedURL.RawPath = strings.TrimPrefix(copiedURL.RawPath, base)
		}
		request.URL = &copiedURL
		handler.ServeHTTP(c.Writer, request)
	}
	g.Any("/icon", serve)
	g.Any("/icon/*path", serve)
}
