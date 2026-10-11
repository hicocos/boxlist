package handles

import (
	"github.com/OpenListTeam/OpenList/v4/internal/derivatives"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
)

func DerivativeWorkers(c *gin.Context) {
	common.SuccessResp(c, derivatives.Default.WorkerConfiguration())
}
func DerivativeSaveWorkers(c *gin.Context) {
	var req struct {
		Workers *int `json:"workers"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Workers == nil || *req.Workers < 1 || *req.Workers > 4 {
		common.ErrorStrResp(c, "invalid_derivative_worker_limit", 400)
		return
	}
	view, e := derivatives.Default.SaveWorkerConfiguration(c.Request.Context(), *req.Workers)
	if !derivativeError(c, e) {
		common.SuccessResp(c, view)
	}
}
