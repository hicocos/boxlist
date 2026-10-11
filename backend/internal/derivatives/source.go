package derivatives

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	internalnet "github.com/OpenListTeam/OpenList/v4/internal/net"
	"github.com/OpenListTeam/OpenList/v4/internal/op"
	"github.com/OpenListTeam/OpenList/v4/internal/stream"
	"github.com/OpenListTeam/OpenList/v4/pkg/http_range"
	"github.com/OpenListTeam/OpenList/v4/server/common"
)

type boundStorage struct {
	ID      uint
	Binding string
	Storage *model.Storage
	Driver  driver.Driver
}

func (m *Manager) checkPermission(ctx context.Context, id uint, p string) error {
	u, e := m.creator(ctx, id)
	if e != nil {
		return e
	}
	base := u.BasePath
	if base == "" {
		base = "/"
	}
	if !inside(strings.TrimSuffix(base, "/"), p) && base != "/" {
		return ErrPermission
	}
	var metas []model.Meta
	if e = m.db.WithContext(ctx).Find(&metas).Error; e != nil {
		return e
	}
	var nearest *model.Meta
	for candidate := p; ; candidate = path.Dir(candidate) {
		for i := range metas {
			if strings.EqualFold(metas[i].Path, candidate) {
				if nearest != nil {
					return ErrPermission
				}
				nearest = &metas[i]
			}
		}
		if nearest != nil || candidate == "/" {
			break
		}
	}
	if !common.CanAccess(u, nearest, p, "") {
		return ErrPermission
	}
	return nil
}
func (m *Manager) resolve(p string) (*boundStorage, string, error) {
	rows, e := m.storages(m.ctx)
	if e != nil {
		return nil, "", e
	}
	s, e := storageForPath(rows, p)
	if e != nil {
		return nil, "", e
	}
	d, actual, e := op.GetStorageAndActualPath(p)
	if e != nil {
		return nil, "", e
	}
	if d.GetStorage().ID != s.ID || Binding(d.GetStorage()) != Binding(s) {
		return nil, "", ErrBinding
	}
	b := &boundStorage{ID: s.ID, Binding: Binding(s), Storage: s, Driver: d}
	if e = checkLocalPath(b, actual); e != nil {
		return nil, "", e
	}
	return b, actual, nil
}
func (m *Manager) validateJob(ctx context.Context, j Job, p string) (*boundStorage, string, error) {
	if e := m.checkPermission(ctx, j.Creator, p); e != nil {
		return nil, "", e
	}
	if !inside(j.Path, p) {
		return nil, "", ErrInvalidPath
	}
	b, actual, e := m.resolve(p)
	if e != nil {
		return nil, "", e
	}
	if b.ID != j.StorageID || b.Binding != j.Binding {
		return nil, "", ErrBinding
	}
	if j.RuleID != 0 {
		var r Rule
		if e = m.db.WithContext(ctx).First(&r, j.RuleID).Error; e != nil {
			return nil, "", ErrBinding
		}
		if r.Binding != j.Binding || r.Creator != j.Creator || !inside(r.Path, p) {
			return nil, "", ErrBinding
		}
	}
	return b, actual, nil
}
func scanContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, ownScanKey{}, true)
}
func (m *Manager) fresh(ctx context.Context, b *boundStorage, actual string) (model.Obj, error) {
	if e := checkLocalPath(b, actual); e != nil {
		return nil, e
	}
	ctx = scanContext(ctx)
	if g, ok := b.Driver.(driver.Getter); ok {
		observedAt := time.Now()
		o, e := g.Get(ctx, actual)
		if e == nil {
			return model.StampObjObservation(o, observedAt), nil
		}
		if !errs.IsNotImplementError(e) && !errs.IsNotSupportError(e) {
			return nil, e
		}
	}
	if actual == "/" {
		return op.Get(ctx, b.Driver, actual)
	}
	objs, e := op.List(ctx, b.Driver, path.Dir(actual), model.ListArgs{ReqPath: path.Dir(actual), Refresh: true, SkipHook: true})
	if e != nil {
		return nil, e
	}
	for _, o := range objs {
		if o.GetName() == path.Base(actual) {
			return o, nil
		}
	}
	return nil, errs.ObjectNotFound
}

// Local drivers can follow symlinks before returning Obj metadata. Check every
// path component ourselves and reject even in-root symlinks to eliminate loops.
func checkLocalPath(b *boundStorage, actual string) error {
	if !strings.EqualFold(b.Storage.Driver, "Local") {
		return nil
	}
	r, ok := b.Driver.(driver.IRootPath)
	if !ok {
		return ErrUnsupportedStorage
	}
	root, e := filepath.Abs(r.GetRootPath())
	if e != nil {
		return e
	}
	p := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(actual, "/")))
	if !localInside(root, p) {
		return ErrInvalidPath
	}
	return noSymlinks(p)
}

func sourceURLAllowed(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return errors.New("invalid_source_url")
	}
	// Do not use the public same-site proxy even when a reference driver provides
	// it as an absolute URL. Private external source servers remain supported.
	if conf.Conf != nil && conf.Conf.SiteURL != "" {
		site, e := url.Parse(conf.Conf.SiteURL)
		if e == nil && strings.EqualFold(site.Hostname(), u.Hostname()) {
			return errors.New("source_url_loops_to_site")
		}
	}
	host := strings.ToLower(u.Hostname())
	if (host == "localhost" || host == "127.0.0.1" || host == "::1") && (u.Port() == "5244" || u.Port() == "") {
		return errors.New("source_url_loops_to_site")
	}
	return nil
}

type sourceHTTPError struct {
	Status int
	After  time.Duration
}

func (e *sourceHTTPError) Error() string { return fmt.Sprintf("source_http_%d", e.Status) }
func retryAfter(header string, now time.Time) time.Duration {
	if seconds, e := strconv.ParseInt(strings.TrimSpace(header), 10, 64); e == nil {
		if seconds < 0 {
			return 0
		}
		if seconds > 86400 {
			seconds = 86400
		}
		return time.Duration(seconds) * time.Second
	}
	if t, e := http.ParseTime(header); e == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0
		}
		if d > 24*time.Hour {
			return 24 * time.Hour
		}
		return d
	}
	return 0
}

func openSource(ctx context.Context, link *model.Link, size int64) (io.ReadCloser, error) {
	if link == nil {
		return nil, errors.New("invalid_source_link")
	}
	if link.RangeReader != nil {
		copy := *link
		copy.Concurrency = 0
		copy.PartSize = 0
		reader, e := stream.GetRangeReaderFromLink(size, &copy)
		if e != nil {
			return nil, e
		}
		return reader.RangeRead(ctx, http_range.Range{Start: 0, Length: -1})
	}
	if e := sourceURLAllowed(link.URL); e != nil {
		return nil, e
	}
	// RequestHttp discards Retry-After and buffers an unbounded error body. Use
	// the same internal transport but retain status/headers and close immediately.
	client := *internalnet.HttpClient()
	oldRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if e := sourceURLAllowed(req.URL.String()); e != nil {
			return e
		}
		if oldRedirect != nil {
			return oldRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("source_redirect_limit")
		}
		return nil
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, link.URL, nil)
	if e != nil {
		return nil, e
	}
	req.Header = link.Header.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	req.Header.Set("Accept-Encoding", "identity")
	res, e := client.Do(req)
	if e != nil {
		return nil, e
	}
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusPartialContent {
		res.Body.Close()
		return nil, &sourceHTTPError{Status: res.StatusCode, After: retryAfter(res.Header.Get("Retry-After"), time.Now())}
	}
	if res.ContentLength > maxDownload {
		res.Body.Close()
		return nil, &EncodeError{Code: "skipped_limit", Err: errors.New("source exceeds download limit")}
	}
	return res.Body, nil
}

func (m *Manager) download(ctx context.Context, b *boundStorage, obj model.Obj, output string) (int64, error) {
	if obj.GetSize() > maxDownload {
		return 0, &EncodeError{Code: "skipped_limit", Err: errors.New("source exceeds download limit")}
	}
	// Resolve a fresh internal driver link rather than persisting signed URLs or
	// using fs.Link, which can rewrite relative links into the public /d endpoint.
	link, e := b.Driver.Link(ctx, model.UnwrapObjName(obj), model.LinkArgs{Header: make(http.Header)})
	if e != nil {
		return 0, e
	}
	if link == nil {
		return 0, errors.New("invalid_source_link")
	}
	defer link.Close()
	reader, e := openSource(ctx, link, obj.GetSize())
	if e != nil {
		return 0, e
	}
	defer reader.Close()
	// Close an in-flight remote reader on cancellation; checking ctx only between
	// Read calls is insufficient when a driver blocks inside Read.
	stop := context.AfterFunc(ctx, func() { reader.Close() })
	defer stop()
	f, e := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return 0, e
	}
	defer f.Close()
	n, e := io.CopyBuffer(f, io.LimitReader(m.pacedSource(ctx, reader), maxDownload+1), make([]byte, 64<<10))
	if n > maxDownload {
		return n, &EncodeError{Code: "skipped_limit", Err: errors.New("source exceeds download limit")}
	}
	if e != nil {
		return n, e
	}
	if obj.GetSize() > 0 && obj.GetSize() != n {
		return n, io.ErrUnexpectedEOF
	}
	if n == 0 {
		return n, &EncodeError{Code: "invalid_image", Err: errors.New("empty source")}
	}
	if e = f.Sync(); e != nil {
		return n, e
	}
	return n, f.Close()
}
