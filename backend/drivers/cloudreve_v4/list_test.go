package cloudreve_v4

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenListTeam/OpenList/v4/drivers/base"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/go-resty/resty/v2"
)

func testDriver(t *testing.T, handler http.HandlerFunc) *CloudreveV4 {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	old := base.RestyClient
	base.RestyClient = resty.New().SetRetryCount(3).SetTimeout(5 * time.Second)
	t.Cleanup(func() { base.RestyClient = old })
	return &CloudreveV4{Addition: Addition{Address: srv.URL, OrderBy: "name", OrderDirection: "asc"}}
}

func replyData(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
}

func fixtureFiles(start, n, typ int) []File {
	files := make([]File, n)
	for i := range files {
		name := fmt.Sprintf("entry-%04d", start+i)
		files[i] = File{ID: name, Name: name, Path: "cloudreve://my/" + name, Type: typ, Size: 42}
	}
	return files
}

func listTestDir(d *CloudreveV4, ctx context.Context) ([]model.Obj, error) {
	return d.List(ctx, &model.Object{Path: "cloudreve://my", IsFolder: true}, model.ListArgs{})
}

func TestListNumericPagination(t *testing.T) {
	for _, tc := range []struct {
		name        string
		total, size int
		reportTotal bool
	}{
		{"230_entries", 230, 100, true},
		{"345_entries", 345, 100, true},
		{"exact_multiple", 200, 100, true},
		{"empty", 0, 100, true},
		{"server_cap", 7, 2, true},
		{"unknown_total", 7, 2, false},
		{"unknown_exact_multiple", 4, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var pages []int
			d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v4/file" {
					t.Errorf("unexpected enrichment request: %s", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				pages = append(pages, page)
				start := page * tc.size
				n := min(tc.size, max(0, tc.total-start))
				p := map[string]any{"page": page, "page_size": tc.size}
				if tc.reportTotal {
					p["total_items"] = tc.total
				}
				replyData(w, map[string]any{"files": fixtureFiles(start, n, 1), "pagination": p})
			})
			objs, err := listTestDir(d, context.Background())
			if err != nil || len(objs) != tc.total {
				t.Fatalf("got %d entries, err=%v", len(objs), err)
			}
			seen := map[string]bool{}
			for i, obj := range objs {
				if obj.GetName() != fmt.Sprintf("entry-%04d", i) || seen[obj.GetName()] {
					t.Fatalf("duplicate/out-of-order: %s", obj.GetName())
				}
				seen[obj.GetName()] = true
			}
			wantPages := max(1, (tc.total+tc.size-1)/tc.size)
			if !tc.reportTotal && tc.total%tc.size == 0 {
				wantPages++
			}
			if len(pages) != wantPages {
				t.Fatalf("pages=%v want %d requests", pages, wantPages)
			}
		})
	}
}

func TestListCursorPagination(t *testing.T) {
	for _, finalSize := range []int{1, 100} {
		t.Run(strconv.Itoa(finalSize), func(t *testing.T) {
			var tokens []string
			d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
				token := r.URL.Query().Get("next_page_token")
				tokens = append(tokens, token)
				p := map[string]any{"page": 0, "page_size": 100, "is_cursor": true}
				start, n := 0, 2 // A short page WITH a token must still continue.
				if token == "" {
					p["next_token"] = "next"
				} else {
					start, n = 2, finalSize
				}
				replyData(w, map[string]any{"files": fixtureFiles(start, n, 1), "pagination": p})
			})
			objs, err := listTestDir(d, context.Background())
			if err != nil || len(objs) != 2+finalSize || !reflect.DeepEqual(tokens, []string{"", "next"}) {
				t.Fatalf("count=%d tokens=%v err=%v", len(objs), tokens, err)
			}
		})
	}
}

func TestListInvalidPaginationDoesNotReturnPartialSuccess(t *testing.T) {
	for _, mode := range []string{"repeated_token", "empty_before_total", "wrong_page", "second_page_error"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				p := map[string]any{"page": calls - 1, "page_size": 1, "total_items": 3}
				files := fixtureFiles(calls-1, 1, 1)
				if mode == "repeated_token" {
					p["is_cursor"] = true
					p["next_token"] = "same"
				}
				if calls == 2 {
					switch mode {
					case "empty_before_total":
						files = nil
					case "wrong_page":
						p["page"] = 0
					case "second_page_error":
						w.WriteHeader(500)
						return
					}
				}
				replyData(w, map[string]any{"files": files, "pagination": p})
			})
			objs, err := listTestDir(d, context.Background())
			if err == nil || objs != nil || calls != 2 {
				t.Fatalf("partial success/infinite pagination: count=%d calls=%d err=%v", len(objs), calls, err)
			}
		})
	}
}

func TestCancellationReachesUpstream(t *testing.T) {
	for _, mode := range []string{"list", "get", "link", "folder_summary", "thumbnail", "login", "reference"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{}, 1)
			stopped := make(chan struct{}, 1)
			var calls atomic.Int32
			d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if (mode == "folder_summary" || mode == "thumbnail") && r.URL.Path == "/api/v4/file" {
					typ := 0
					if mode == "folder_summary" {
						typ = 1
					}
					replyData(w, map[string]any{"files": fixtureFiles(0, 2, typ), "pagination": map[string]any{"total_items": 2}})
					return
				}
				started <- struct{}{}
				select {
				case <-r.Context().Done():
					stopped <- struct{}{}
				case <-time.After(3 * time.Second):
					t.Error("upstream request not cancelled")
				}
			})
			d.EnableFolderSize = mode == "folder_summary"
			d.EnableThumb = mode == "thumbnail"
			if mode == "login" {
				d.Username, d.Password = "test", "test"
			}
			if mode == "reference" {
				d = &CloudreveV4{ref: d}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				switch mode {
				case "get":
					_, err = d.Get(ctx, "/test")
				case "link":
					_, err = d.Link(ctx, &model.Object{Path: "cloudreve://my/test"}, model.LinkArgs{})
				default:
					_, err = listTestDir(d, ctx)
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("request did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("want context cancellation, got %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("driver did not stop promptly")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("upstream did not observe cancellation")
			}
			wantCalls := int32(1)
			if mode == "folder_summary" || mode == "thumbnail" {
				wantCalls++
			}
			if calls.Load() != wantCalls {
				t.Fatalf("unexpected follow-up requests: %d", calls.Load())
			}
		})
	}
}

func TestPreCancelledListDoesNotRequest(t *testing.T) {
	var calls atomic.Int32
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := listTestDir(d, ctx)
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 {
		t.Fatalf("calls=%d err=%v", calls.Load(), err)
	}
}

func TestOptionalMetadataFailurePreservesFiles(t *testing.T) {
	calls := 0
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/api/v4/file" {
			replyData(w, map[string]any{"files": fixtureFiles(0, 1, 0), "pagination": map[string]any{"total_items": 1}})
			return
		}
		w.WriteHeader(500)
	})
	d.EnableThumb = true
	objs, err := listTestDir(d, context.Background())
	if err != nil || len(objs) != 1 || objs[0].GetSize() != 42 || calls != 2 {
		t.Fatalf("count=%d calls=%d err=%v", len(objs), calls, err)
	}
}

func TestOptionalMetadataDeadlineDoesNotCachePartialList(t *testing.T) {
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/file" {
			replyData(w, map[string]any{"files": fixtureFiles(0, 1, 0), "pagination": map[string]any{"total_items": 1}})
			return
		}
		<-r.Context().Done()
	})
	d.EnableThumb = true
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	objs, err := listTestDir(d, ctx)
	elapsed := time.Since(start)
	if !errors.Is(err, context.DeadlineExceeded) || objs != nil || elapsed > time.Second {
		t.Fatalf("count=%d duration=%s err=%v", len(objs), elapsed, err)
	}
}

func TestLoginRequiredDoesNotRecurseOnLoginEndpoint(t *testing.T) {
	calls := 0
	d := testDriver(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"code": CodeLoginRequired, "msg": "login required"})
	})
	d.Username, d.Password = "test", "test"
	_, err := listTestDir(d, context.Background())
	if err == nil || calls != 1 {
		t.Fatalf("calls=%d err=%v", calls, err)
	}
}
