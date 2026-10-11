package handles

import (
	"github.com/OpenListTeam/OpenList/v4/internal/derivatives"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
)

func DerivativeBackup(c *gin.Context) {
	backup, err := derivatives.Default.BackupS3Previews(c.Request.Context())
	if !derivativeError(c, err) {
		common.SuccessResp(c, backup)
	}
}

func DerivativeRestore(c *gin.Context) {
	var req struct {
		Backup   *derivatives.S3PreviewBackup `json:"backup"`
		Override bool                         `json:"override"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Backup == nil {
		common.ErrorStrResp(c, "derivative_backup_invalid", 400)
		return
	}
	if !derivativeError(c, derivatives.Default.RestoreS3Previews(c.Request.Context(), *req.Backup, req.Override)) {
		common.SuccessResp(c)
	}
}
