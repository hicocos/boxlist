package op

import (
	"context"
	"github.com/OpenListTeam/OpenList/v4/internal/driver"
	"github.com/OpenListTeam/OpenList/v4/pkg/utils"
	"sync"
)

// File mutation observers never resolve/list/download a source. Keep them local
// and lightweight; heavy work must be enqueued. Independent of the optional
// historical post-write recursive indexing hook.
type FileMutationHook func(context.Context, uint, string)

var mutationMu sync.RWMutex
var mutationHooks []FileMutationHook

func RegisterFileMutationHook(h FileMutationHook) {
	mutationMu.Lock()
	defer mutationMu.Unlock()
	mutationHooks = append(mutationHooks, h)
}
func notifyFileMutation(ctx context.Context, storage driver.Driver, relative string) {
	mutationMu.RLock()
	hooks := append([]FileMutationHook(nil), mutationHooks...)
	mutationMu.RUnlock()
	full := utils.GetFullPath(storage.GetStorage().MountPath, relative)
	for _, h := range hooks {
		h(ctx, storage.GetStorage().ID, full)
	}
}
