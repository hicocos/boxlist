package fs

import (
	"context"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/errs"
	"github.com/pkg/errors"
)

func TestListUnmountedRoot(t *testing.T) {
	ctx := context.Background()
	root, err := Get(ctx, "/", &GetArgs{NoLog: true})
	if err != nil || !root.IsDir() {
		t.Fatalf("virtual root must exist: root=%v, err=%v", root, err)
	}
	for _, refresh := range []bool{false, true} {
		objs, err := List(ctx, "/", &ListArgs{Refresh: refresh, NoLog: true})
		if err != nil || len(objs) != 0 {
			t.Fatalf("empty root (refresh=%v): objs=%v, err=%v", refresh, objs, err)
		}
	}
	_, err = List(ctx, "/missing", &ListArgs{NoLog: true})
	if !errors.Is(err, errs.StorageNotFound) {
		t.Fatalf("missing subdirectory must remain an error, got %v", err)
	}
}
