package handles

// Derivative tickets are short-lived, purpose-specific grants, not public file
// names. Every read (including a conditional 304) checks current local ACL state.
// Nothing in this file opens a source link or invokes an encoder.
import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/db"
	"github.com/OpenListTeam/OpenList/v4/internal/derivatives"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
)

const derivativeTicketTTL = 15 * time.Minute

type DerivativeView struct {
	State   string `json:"state"`
	URL     string `json:"url"`
	Width   int    `json:"width"`
	Height  int    `json:"height"`
	Bytes   int64  `json:"bytes"`
	Version int    `json:"version"`
}

type derivativeTicket struct {
	ID      string `json:"i"`
	Profile string `json:"p"`
	User    uint   `json:"u,omitempty"`
	Session string `json:"x,omitempty"`
	Native  bool   `json:"n,omitempty"`
	Share   string `json:"s,omitempty"`
	Grant   string `json:"g"`
	Expires int64  `json:"e"`
}

func derivativeMAC(value []byte) []byte {
	h := hmac.New(sha256.New, []byte(conf.Conf.JwtSecret))
	h.Write([]byte("boxlist/derivatives/v1\x00"))
	h.Write(value)
	return h.Sum(nil)
}

func signDerivativeTicket(ticket derivativeTicket) string {
	b, _ := json.Marshal(ticket)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(derivativeMAC(b))
}

func parseDerivativeTicket(value string) (derivativeTicket, error) {
	var ticket derivativeTicket
	if len(value) > 2048 {
		return ticket, errors.New("invalid ticket")
	}
	body, sig, ok := strings.Cut(value, ".")
	if !ok {
		return ticket, errors.New("invalid ticket")
	}
	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return ticket, errors.New("invalid ticket")
	}
	s, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(s, derivativeMAC(b)) {
		return ticket, errors.New("invalid ticket")
	}
	if json.Unmarshal(b, &ticket) != nil || ticket.User == 0 || ticket.ID == "" || (ticket.Profile != "thumb" && ticket.Profile != "reader") {
		return ticket, errors.New("invalid ticket")
	}
	now := time.Now().Unix()
	if ticket.Expires <= now || ticket.Expires > now+int64(derivativeTicketTTL/time.Second)+30 {
		return ticket, errors.New("expired ticket")
	}
	return ticket, nil
}

// Exact case and Unicode are preserved for filesystem scopes. ACL metadata
// itself follows OpenList's existing case-insensitive nearest-meta semantics.
func derivativeWithin(root, name string) bool {
	root, name = path.Clean(root), path.Clean(name)
	return root == "/" || name == root || strings.HasPrefix(name, root+"/")
}

func validDerivativePath(value string) bool {
	if value == "" || len(value) > 4096 || !strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\x00\\") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "." || part == ".." {
			return false
		}
	}
	return path.Clean(value) == value
}

func derivativeNearestMeta(metas []model.Meta, name string) (*model.Meta, error) {
	for {
		var match *model.Meta
		for i := range metas {
			if strings.EqualFold(path.Clean(metas[i].Path), name) {
				if match != nil {
					return nil, errors.New("ambiguous ACL")
				}
				match = &metas[i]
			}
		}
		if match != nil {
			return match, nil
		}
		if name == "/" {
			return nil, nil
		}
		name = path.Dir(name)
	}
}

// Only security-relevant fields enter the grant. Passwords are HMACed, never
// embedded in a ticket, URL, response, or diagnostic.
func derivativeGrant(user *model.User, meta *model.Meta, share *model.Sharing, name string) string {
	payload := struct {
		ID                 uint
		PasswordTS         int64
		PasswordHash, Base string
		Role               int
		Permission         int32
		Meta               any
		Share              any
	}{ID: user.ID, PasswordTS: user.PwdTS, PasswordHash: user.PwdHash, Base: user.BasePath, Role: user.Role, Permission: user.Permission}
	if meta != nil {
		payload.Meta = []any{meta.ID, meta.Path, meta.Password, meta.PSub, meta.ReadUsers, meta.ReadUsersSub, meta.Hide, meta.HSub}
	}
	if share != nil {
		payload.Share = []any{share.ID, share.Files, share.Pwd, share.Expires, share.MaxAccessed, share.CreatorId}
	}
	b, _ := json.Marshal([]any{payload, name})
	return hex.EncodeToString(derivativeMAC(b))
}

func freshDerivativeShare(id string) (*model.Sharing, error) {
	s, err := db.GetSharingById(id)
	if err != nil {
		return nil, errors.New("share unavailable")
	}
	u, err := db.GetUserById(s.CreatorId)
	if err != nil || u.Disabled {
		return nil, errors.New("share unavailable")
	}
	var files []string
	if json.Unmarshal([]byte(s.FilesRaw), &files) != nil {
		return nil, errors.New("share unavailable")
	}
	share := &model.Sharing{SharingDB: s, Creator: u, Files: files}
	if !share.Valid() {
		return nil, errors.New("share unavailable")
	}
	for _, f := range files {
		if !validDerivativePath(f) || (!u.IsAdmin() && !derivativeWithin(u.BasePath, f)) {
			return nil, errors.New("share scope revoked")
		}
	}
	return share, nil
}

func derivativeShareContains(s *model.Sharing, name string) bool {
	for _, f := range s.Files {
		if derivativeWithin(f, name) {
			return true
		}
	}
	return false
}

func derivativeAccessAllowed(user *model.User, meta *model.Meta, share *model.Sharing, name, password string, granted bool) bool {
	if user == nil || user.Disabled {
		return false
	}
	if share != nil {
		if !share.Valid() || !derivativeShareContains(share, name) {
			return false
		}
	} else if !user.IsAdmin() && !derivativeWithin(user.BasePath, name) {
		return false
	}
	// A matching signed ACL fingerprint proves the previous password check.
	// A valid share is an explicit grant by its owner; still enforce the owner's
	// current base path, hidden-file rules and read-user ACLs.
	if (granted || share != nil) && meta != nil {
		password = meta.Password
	}
	return common.CanAccess(user, meta, name, password)
}

func derivativeViews(c *gin.Context, parent string, objs []model.Obj, password string, share *model.Sharing) map[string]map[string]DerivativeView {
	result := map[string]map[string]DerivativeView{}
	if derivatives.Default == nil || len(objs) == 0 {
		return result
	}
	artifacts, err := derivatives.Default.Lookup(c.Request.Context(), parent, objs)
	if err != nil || len(artifacts) == 0 {
		return result
	}
	return derivativeViewsForArtifacts(c, artifacts, password, share)
}

func derivativeFreshUser(c *gin.Context) (*model.User, error) {
	current, _ := c.Request.Context().Value(conf.UserKey).(*model.User)
	if current == nil {
		return nil, errors.New("access denied")
	}
	u, err := db.GetUserById(current.ID)
	if err != nil || u.Disabled {
		return nil, errors.New("access denied")
	}
	if token := c.GetHeader("Authorization"); token != "" {
		native, nativeErr := db.GetSettingItemByKey(conf.Token)
		if nativeErr == nil && native.Value != "" && hmac.Equal([]byte(token), []byte(native.Value)) {
			if !u.IsAdmin() {
				return nil, errors.New("access denied")
			}
		} else {
			claims, parseErr := common.ParseToken(token)
			if parseErr != nil || claims.Username != u.Username || claims.PwdTS != u.PwdTS {
				return nil, errors.New("access denied")
			}
		}
	} else if !u.IsGuest() {
		return nil, errors.New("access denied")
	}
	return u, nil
}

func derivativeViewsForArtifacts(c *gin.Context, artifacts map[string]map[string]derivatives.Artifact, password string, share *model.Sharing) map[string]map[string]DerivativeView {
	result := map[string]map[string]DerivativeView{}
	var user *model.User
	var err error
	if share != nil {
		share, err = freshDerivativeShare(share.ID)
		if err != nil || !share.Verify(password) {
			return result
		}
		user = share.Creator
	} else {
		user, err = derivativeFreshUser(c)
		if err != nil {
			return result
		}
	}
	var session string
	var nativeSession bool
	expires := time.Now().Add(derivativeTicketTTL)
	if share == nil && !user.IsGuest() {
		token := c.GetHeader("Authorization")
		native, nativeErr := db.GetSettingItemByKey(conf.Token)
		nativeSession = nativeErr == nil && native.Value != "" && hmac.Equal([]byte(token), []byte(native.Value))
		if !nativeSession {
			claims, e := common.ParseToken(token)
			if e != nil || claims.ExpiresAt == nil {
				return result
			}
			if claims.ExpiresAt.Time.Before(expires) {
				expires = claims.ExpiresAt.Time
			}
		}
		if nativeSession {
			session = derivativeSessionFingerprint(token)
			if !derivativeNativeSessionValid(session) {
				return result
			}
		} else {
			session = derivativeRememberSession(token, expires)
		}
		if session == "" {
			return result
		}
	}
	metas, err := db.GetAllMetas()
	if err != nil {
		return result
	}
	for key, profiles := range artifacts {
		for profile, artifact := range profiles {
			if artifact.State != "ready" {
				continue
			}
			meta, err := derivativeNearestMeta(metas, artifact.Path)
			if err != nil || !derivativeAccessAllowed(user, meta, share, artifact.Path, password, false) {
				continue
			}
			ticket := derivativeTicket{ID: artifact.ID, Profile: profile, User: user.ID, Session: session, Native: nativeSession, Grant: derivativeGrant(user, meta, share, artifact.Path), Expires: expires.Unix()}
			if share != nil {
				ticket.Share = share.ID
			}
			if result[key] == nil {
				result[key] = map[string]DerivativeView{}
			}
			deliveryURL := strings.TrimSuffix(common.GetApiUrl(c), "/") + "/api/fs/derivatives/" + url.PathEscape(artifact.ID) + "/" + profile + "?ticket=" + url.QueryEscape(signDerivativeTicket(ticket))
			if direct := derivatives.Default.PublicArtifactURL(artifact); direct != "" {
				deliveryURL = direct
			}
			result[key][profile] = DerivativeView{State: "ready", URL: deliveryURL, Width: artifact.Width, Height: artifact.Height, Bytes: artifact.Bytes, Version: artifact.Version}
		}
	}
	return result
}

func overlayDerivativeViews(c *gin.Context, response []ObjResp, objs []model.Obj, parent, password string, share *model.Sharing) []ObjResp {
	views := derivativeViews(c, parent, objs, password, share)
	for i := range response {
		response[i].Derivatives = views[response[i].Name]
	}
	return response
}

// Sharing can expose an artificial multi-file root. Group by real parent and
// batch each group; do not resolve every object against a remote driver.
func sharingDerivativeViews(c *gin.Context, share *model.Sharing, relative string, objs []model.Obj, password string, single bool) map[string]map[string]DerivativeView {
	result := map[string]map[string]DerivativeView{}
	if derivatives.Default == nil {
		return result
	}
	if !single && (relative == "" || relative == "/") && len(share.Files) > 1 {
		groups := map[string][]model.Obj{}
		for _, obj := range objs {
			if obj.IsDir() {
				continue
			}
			for _, f := range share.Files {
				if path.Base(f) == obj.GetName() {
					groups[path.Dir(f)] = append(groups[path.Dir(f)], obj)
					break
				}
			}
		}
		for parent, objects := range groups {
			for name, profiles := range derivativeViews(c, parent, objects, password, share) {
				result[name] = profiles
			}
		}
		return result
	}
	actual, err := op.GetSharingUnwrapPath(share, relative)
	if err != nil {
		return result
	}
	if single {
		actual = path.Dir(actual)
	}
	return derivativeViews(c, actual, objs, password, share)
}

func ReadDerivative(c *gin.Context) {
	c.Header("Cache-Control", "private, no-cache")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	deny := func(code int) { c.AbortWithStatus(code) }
	if derivatives.Default == nil {
		deny(503)
		return
	}
	token := c.GetString("derivative_ticket")
	if token == "" {
		token = c.Query("ticket")
	}
	ticket, err := parseDerivativeTicket(token)
	if err != nil || ticket.ID != c.Param("id") || ticket.Profile != c.Param("profile") {
		deny(403)
		return
	}
	artifact, err := derivatives.Default.Artifact(ticket.ID, ticket.Profile)
	if err != nil || artifact.State != "ready" {
		deny(404)
		return
	}
	user, err := db.GetUserById(ticket.User)
	if err != nil || user.Disabled {
		deny(403)
		return
	}
	if ticket.Share == "" && !user.IsGuest() {
		if !ticket.Native && !derivativeSessionValid(ticket.Session) {
			deny(403)
			return
		}
		if ticket.Native {
			if !derivativeNativeSessionValid(ticket.Session) {
				deny(403)
				return
			}
			native, e := db.GetSettingItemByKey(conf.Token)
			if e != nil || native.Value == "" || derivativeSessionFingerprint(native.Value) != ticket.Session {
				deny(403)
				return
			}
		}
	}
	var share *model.Sharing
	if ticket.Share != "" {
		share, err = freshDerivativeShare(ticket.Share)
		if err != nil || share.CreatorId != user.ID {
			deny(403)
			return
		}
	}
	metas, err := db.GetAllMetas()
	if err != nil {
		deny(503)
		return
	}
	meta, err := derivativeNearestMeta(metas, artifact.Path)
	if err != nil || !hmac.Equal([]byte(ticket.Grant), []byte(derivativeGrant(user, meta, share, artifact.Path))) || !derivativeAccessAllowed(user, meta, share, artifact.Path, "", true) {
		deny(403)
		return
	}
	storage, err := db.GetStorageById(artifact.StorageID)
	if err != nil || storage.Disabled || derivatives.Binding(storage) != artifact.Binding {
		deny(403)
		return
	}
	// A changed nested mount must not make the previous storage's output visible.
	active, _, err := op.GetStorageAndActualPath(artifact.Path)
	if err != nil || active.GetStorage().ID != artifact.StorageID {
		deny(403)
		return
	}
	if artifact.OutputTargetID != 0 {
		serveRemoteDerivative(c, artifact)
		return
	}
	filename, err := derivatives.Default.FilePath(artifact)
	if err != nil {
		deny(404)
		return
	}
	f, err := os.Open(filename)
	if err != nil {
		deny(404)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Bytes {
		deny(404)
		return
	}
	c.Header("Content-Type", "image/webp")
	c.Header("ETag", "\""+artifact.SHA256+"\"")
	// ServeContent handles If-None-Match/Range/HEAD only after the ACL checks.
	http.ServeContent(c.Writer, c.Request, "preview.webp", info.ModTime(), f)
}

func DerivativeStatus(c *gin.Context) {
	var req struct {
		Path     string   `json:"path"`
		Password string   `json:"password"`
		Names    []string `json:"names"`
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	if c.ShouldBindJSON(&req) != nil || !validDerivativePath(req.Path) || len(req.Names) > 200 {
		common.ErrorStrResp(c, "Invalid preview status request", 400)
		return
	}
	if derivatives.Default == nil {
		common.SuccessResp(c, map[string]any{})
		return
	}
	var share *model.Sharing
	parent := req.Path
	var err error
	if strings.HasPrefix(req.Path, "/@s/") {
		sid, rest, _ := strings.Cut(strings.TrimPrefix(req.Path, "/@s/"), "/")
		share, err = freshDerivativeShare(sid)
		if err != nil || !share.Verify(req.Password) {
			common.ErrorStrResp(c, "Share access denied", 403)
			return
		}
		if rest == "" && len(share.Files) != 1 {
			common.SuccessResp(c, map[string]any{})
			return
		}
		parent, err = op.GetSharingUnwrapPath(share, "/"+rest)
	} else {
		user, freshErr := derivativeFreshUser(c)
		if freshErr != nil {
			common.ErrorStrResp(c, "Access denied", 403)
			return
		}
		parent, err = user.JoinPath(req.Path)
	}
	if err != nil {
		common.ErrorStrResp(c, "Access denied", 403)
		return
	}
	metas, err := db.GetAllMetas()
	if err != nil {
		common.ErrorStrResp(c, "Authorization unavailable", 503)
		return
	}
	var user *model.User
	if share != nil {
		user = share.Creator
	} else {
		user, err = derivativeFreshUser(c)
		if err != nil {
			common.ErrorStrResp(c, "Access denied", 403)
			return
		}
	}
	meta, err := derivativeNearestMeta(metas, parent)
	if err != nil || !derivativeAccessAllowed(user, meta, share, parent, req.Password, false) {
		common.ErrorStrResp(c, "Access denied", 403)
		return
	}
	paths := make([]string, 0, len(req.Names))
	for _, name := range req.Names {
		if name == "" || name == "." || name == ".." || len(name) > 1024 || strings.ContainsAny(name, "/\\\x00") {
			common.ErrorStrResp(c, "Invalid file name", 400)
			return
		}
		full := path.Join(parent, name)
		fileMeta, metaErr := derivativeNearestMeta(metas, full)
		if metaErr != nil || !derivativeAccessAllowed(user, fileMeta, share, full, req.Password, false) {
			common.ErrorStrResp(c, "Access denied", 403)
			return
		}
		paths = append(paths, full)
	}
	artifacts, err := derivatives.Default.LookupPaths(c.Request.Context(), paths)
	if err != nil {
		common.ErrorStrResp(c, "Preview index unavailable", 503)
		return
	}
	renamed := map[string]map[string]derivatives.Artifact{}
	for name, profiles := range artifacts {
		renamed[path.Base(name)] = profiles
	}
	common.SuccessResp(c, derivativeViewsForArtifacts(c, renamed, req.Password, share))
}

func derivativePage(c *gin.Context) (int, int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	perPage, _ := strconv.Atoi(c.DefaultQuery("per_page", "20"))
	if page < 1 {
		page = 1
	}
	if page > 100000 {
		page = 100000
	}
	if perPage < 1 {
		perPage = 20
	}
	if perPage > 100 {
		perPage = 100
	}
	return page, perPage
}
