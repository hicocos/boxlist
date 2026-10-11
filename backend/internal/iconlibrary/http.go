package iconlibrary

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Hooks must be backed by native authentication, fresh native settings and an
// exact trusted Origin check. No nil hook or environment variable enables writes.
type Hooks struct {
	Authorize   func(*http.Request) (string, error)
	ReadHead    func(*http.Request) (string, error)
	CheckOrigin func(*http.Request) bool
}
type Service struct {
	store     *store
	hooks     Hooks
	mutation  sync.Mutex
	slots     chan struct{}
	deletion  chan struct{}
	pendingMu sync.Mutex
	pending   map[string]bool
	jobsMu    sync.Mutex
	jobs      map[string]TransparencyJob
	activeJob string
	workers   sync.WaitGroup
	closing   bool
}

// New opens a private catalog. Only one native Service may own a data root.
func New(root string, hooks Hooks) (*Service, error) {
	if hooks.Authorize == nil || hooks.ReadHead == nil || hooks.CheckOrigin == nil {
		return nil, errors.New("all icon hooks are required")
	}
	store, err := newStore(root)
	if err != nil {
		return nil, err
	}
	if err = store.installSeeds(builtinFiles); err == nil {
		_, err = store.catalog()
	}
	if err != nil {
		store.close()
		return nil, err
	}
	return &Service{store: store, hooks: hooks, slots: make(chan struct{}, 2), deletion: make(chan struct{}, 1), pending: map[string]bool{}, jobs: map[string]TransparencyJob{}}, nil
}
func (s *Service) Close() error {
	s.jobsMu.Lock()
	s.closing = true
	s.jobsMu.Unlock()
	s.workers.Wait()
	s.mutation.Lock()
	defer s.mutation.Unlock()
	return s.store.close()
}
func safeMessage(status int) string {
	switch status {
	case 400:
		return "请求无效"
	case 401:
		return "请先登录管理员账号"
	case 403:
		return "权限不足"
	case 404:
		return "资源不存在"
	case 405:
		return "请求方法不允许"
	case 408:
		return "请求超时"
	case 409:
		return "正在使用或上传标识冲突"
	case 410:
		return "素材已删除，请使用新上传标识"
	case 411:
		return "缺少文件长度"
	case 413:
		return "文件大小、图片尺寸或动画复杂度超出限制"
	case 415:
		return "仅支持 PNG 静态图片、GIF 图片和原始请求体，文件扩展名须与内容一致"
	case 417:
		return "不支持此请求预期"
	case 422:
		return "图片无效或损坏"
	case 429:
		return "服务繁忙，请稍后重试"
	case 502:
		return "无法核对权限或图标配置"
	case 503:
		return "素材库暂不可用，请使用原标识重试核对"
	case 504:
		return "权限校验超时"
	case 507:
		return "素材库容量已满"
	default:
		return "服务内部错误"
	}
}
func reply(w http.ResponseWriter, status int, data any) {
	b, _ := json.Marshal(data)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if status == 429 || status == 503 {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
func replyError(w http.ResponseWriter, err error) {
	status := 500
	var f Fault
	var fp *Fault
	if errors.As(err, &f) {
		status = f.Status
	} else if errors.As(err, &fp) {
		status = fp.Status
	}
	if status < 400 || status > 599 {
		status = 500
	}
	reply(w, status, map[string]any{"code": status, "message": safeMessage(status)})
}
func checkedHeaders(r *http.Request) error {
	singleton := map[string]bool{"authorization": true, "origin": true, "content-length": true, "content-type": true, "content-encoding": true, "transfer-encoding": true, "expect": true, "x-upload-id": true, "x-icon-name": true, "host": true}
	counts := map[string]int{}
	total := 0
	for key, values := range r.Header {
		k := strings.ToLower(key)
		counts[k] += len(values)
		if singleton[k] && counts[k] > 1 {
			return fault(400)
		}
		for _, value := range values {
			total += len(key) + len(value)
			for _, c := range value {
				if c < 32 || c == 127 {
					return fault(400)
				}
			}
		}
	}
	if total > 32768 || len(r.Header) > 64 {
		return fault(400)
	}
	if len(r.TransferEncoding) > 0 || r.Header.Get("Transfer-Encoding") != "" {
		return fault(400)
	}
	if _, ok := counts["content-encoding"]; ok {
		return fault(415)
	}
	return nil
}
func (s *Service) authorize(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if len(auth) == 0 || len(auth) > 4096 || strings.TrimSpace(auth) != auth {
		return "", fault(401)
	}
	for _, c := range auth {
		if c < 32 || c > 126 {
			return "", fault(401)
		}
	}
	if r.Header.Get("Origin") == "" || !s.hooks.CheckOrigin(r) {
		return "", fault(403)
	}
	id, err := s.hooks.Authorize(r)
	if err != nil {
		return "", err
	}
	if !identityPattern.MatchString(id) {
		return "", fault(502)
	}
	return id, nil
}
func (s *Service) reauthorize(r *http.Request, id string) error {
	current, err := s.authorize(r)
	if err != nil {
		return err
	}
	if current != id {
		return fault(403)
	}
	return nil
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recover() != nil {
			replyError(w, fault(500))
		}
	}()
	err := s.handle(w, r)
	if err != nil {
		replyError(w, err)
	}
}
func (s *Service) handle(w http.ResponseWriter, r *http.Request) error {
	path := r.URL.Path
	// Only asset cache-version queries are supported, never route aliases.
	queryOK := r.URL.RawQuery == ""
	if r.Method == http.MethodGet && strings.HasPrefix(path, "/icon/assets/") && strings.HasPrefix(r.URL.RawQuery, "v=") {
		version := strings.TrimPrefix(r.URL.RawQuery, "v=")
		queryOK = version == "current" || validRevision(version)
	}
	if r.URL.IsAbs() || !queryOK || r.URL.ForceQuery || r.URL.Fragment != "" || r.URL.RawPath != "" || strings.ContainsAny(path, "%\\") || !strings.HasPrefix(path, "/icon/") || strings.Contains(path, "//") || strings.Contains(path, "/../") || strings.Contains(path, "/./") {
		return fault(404)
	}
	if err := checkedHeaders(r); err != nil {
		return err
	}
	if strings.HasPrefix(path, "/icon/api/assets/") && strings.HasSuffix(path, "/transparent") {
		asset := strings.TrimSuffix(strings.TrimPrefix(path, "/icon/api/assets/"), "/transparent")
		if !validAsset(asset) {
			return fault(404)
		}
		return s.handleTransparency(w, r, asset)
	}
	if r.Method == http.MethodGet {
		if r.ContentLength != 0 || (r.Header.Get("Content-Length") != "" && r.Header.Get("Content-Length") != "0") || r.Header.Get("Expect") != "" {
			return fault(400)
		}
		switch path {
		case "/icon/api/health":
			reply(w, 200, map[string]any{"code": 200, "data": map[string]int{"version": 1}})
			return nil
		case "/icon/api/catalog":
			catalog, err := s.store.catalog()
			if err != nil {
				return err
			}
			reply(w, 200, map[string]any{"code": 200, "data": catalog})
			return nil
		}
		if strings.HasPrefix(path, "/icon/assets/") {
			id := strings.TrimPrefix(path, "/icon/assets/")
			requestedSuffix := ""
			for _, suffix := range []string{".webp", ".gif"} {
				if strings.HasSuffix(id, suffix) {
					id = strings.TrimSuffix(id, suffix)
					requestedSuffix = suffix
					break
				}
			}
			if !assetPattern.MatchString(id) {
				return fault(404)
			}
			data, suffix, err := s.store.preview(id)
			if err != nil {
				return err
			}
			if requestedSuffix != "" && requestedSuffix != suffix {
				return fault(404)
			}
			contentType := "image/webp"
			if suffix == ".gif" {
				contentType = "image/gif"
			}
			w.Header().Set("Content-Type", contentType)
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			etag := `"` + contentRevision(data) + `"`
			w.Header().Set("ETag", etag)
			for _, candidate := range strings.Split(r.Header.Get("If-None-Match"), ",") {
				candidate = strings.TrimSpace(candidate)
				if candidate == "*" || strings.TrimPrefix(candidate, "W/") == etag {
					w.WriteHeader(http.StatusNotModified)
					return nil
				}
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			w.WriteHeader(200)
			_, _ = w.Write(data)
			return nil
		}
		return fault(404)
	}
	if r.Method != http.MethodPost && r.Method != http.MethodDelete {
		return fault(405)
	}
	if r.Method == http.MethodPost {
		if path != "/icon/api/upload" {
			return fault(404)
		}
		id, err := s.authorize(r)
		if err != nil {
			return err
		}
		if r.Header.Get("Content-Type") != "application/octet-stream" {
			return fault(415)
		}
		length := r.Header.Get("Content-Length")
		if length == "" {
			return fault(411)
		}
		if len(length) > 10 {
			return fault(400)
		}
		for _, c := range length {
			if c < '0' || c > '9' {
				return fault(400)
			}
		}
		n, err := strconv.ParseInt(length, 10, 64)
		if err != nil || n != r.ContentLength {
			return fault(400)
		}
		if n < 1 || n > MaxInputBytes {
			return fault(413)
		}
		key := r.Header.Get("X-Upload-Id")
		if !requestPattern.MatchString(key) {
			return fault(400)
		}
		name, err := decodeName(r.Header.Get("X-Icon-Name"))
		if err != nil {
			return err
		}
		expect := r.Header.Get("Expect")
		if expect != "" && strings.ToLower(expect) != "100-continue" {
			return fault(417)
		}
		select {
		case s.slots <- struct{}{}:
			defer func() { <-s.slots }()
		default:
			return fault(429)
		}
		controller := http.NewResponseController(w)
		_ = controller.SetReadDeadline(time.Now().Add(20 * time.Second))
		defer controller.SetReadDeadline(time.Time{})
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxInputBytes))
		if err != nil {
			var tooLarge *http.MaxBytesError
			if errors.As(err, &tooLarge) {
				return fault(413)
			}
			if os.IsTimeout(err) {
				return fault(408)
			}
			return fault(400)
		}
		if int64(len(body)) != n {
			return fault(400)
		}
		s.pendingMu.Lock()
		pending := s.pending[uploadID(id, key)]
		s.pendingMu.Unlock()
		if pending {
			return fault(409)
		}
		s.mutation.Lock()
		defer s.mutation.Unlock()
		if err = s.reauthorize(r, id); err != nil {
			return err
		}
		result, exists, err := s.store.retry(id, key, name, body)
		if err != nil {
			return err
		}
		if !exists {
			preview, err := ConvertImage(name, body)
			if err != nil {
				return err
			}
			if err = s.reauthorize(r, id); err != nil {
				return err
			}
			result, err = s.store.commit(id, key, name, body, preview)
			if err != nil {
				return err
			}
		}
		reply(w, 200, map[string]any{"code": 200, "data": result})
		return nil
	}
	prefix := "/icon/api/assets/"
	if !strings.HasPrefix(path, prefix) {
		return fault(404)
	}
	asset := strings.TrimPrefix(path, prefix)
	if !validAsset(asset) {
		return fault(404)
	}
	if r.ContentLength != 0 || (r.Header.Get("Content-Length") != "" && r.Header.Get("Content-Length") != "0") || r.Header.Get("Expect") != "" {
		return fault(400)
	}
	id, err := s.authorize(r)
	if err != nil {
		return err
	}
	select {
	case s.deletion <- struct{}{}:
		defer func() { <-s.deletion }()
	default:
		return fault(429)
	}
	s.pendingMu.Lock()
	s.pending[asset] = true
	s.pendingMu.Unlock()
	defer func() { s.pendingMu.Lock(); delete(s.pending, asset); s.pendingMu.Unlock() }()
	s.mutation.Lock()
	defer s.mutation.Unlock()
	for i := 0; i < 2; i++ {
		if err = s.reauthorize(r, id); err != nil {
			return err
		}
		head, err := s.hooks.ReadHead(r)
		if err != nil {
			return err
		}
		refs, err := HeadReferences(head)
		if err != nil {
			return err
		}
		if refs[asset] {
			return fault(409)
		}
	}
	if err = s.store.delete(asset); err != nil {
		return err
	}
	var builtin bool
	if err = s.store.db.QueryRow("SELECT EXISTS (SELECT 1 FROM builtin_seeds WHERE asset_id=?)", asset).Scan(&builtin); err != nil {
		return fault(503)
	}
	kind := "uploaded"
	if builtin {
		kind = "builtin"
	}
	reply(w, 200, map[string]any{"code": 200, "data": map[string]any{"id": asset, "kind": kind, "removed": true}})
	return nil
}
