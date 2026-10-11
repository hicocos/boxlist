package handles

import (
	"context"
	"github.com/OpenListTeam/OpenList/v4/internal/derivatives"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"net/http"
	"time"
)

func DerivativeS3Configuration(c *gin.Context) {
	value, err := derivatives.Default.S3Configuration()
	if !derivativeError(c, err) {
		common.SuccessResp(c, value)
	}
}
func DerivativeS3Save(c *gin.Context) {
	var req derivatives.S3ConfigRequest
	if c.ShouldBindJSON(&req) != nil {
		common.ErrorStrResp(c, "s3_configuration_invalid", 400)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	value, err := derivatives.Default.SaveS3Configuration(ctx, req)
	if !derivativeError(c, err) {
		common.SuccessResp(c, value)
	}
}
func DerivativeS3Test(c *gin.Context) {
	var req derivatives.S3ConfigRequest
	if c.ShouldBindJSON(&req) != nil {
		common.ErrorStrResp(c, "s3_configuration_invalid", 400)
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	value, err := derivatives.Default.TestS3Configuration(ctx, req)
	if !derivativeError(c, err) {
		common.SuccessResp(c, value)
	}
}

// Called only after fresh ticket, session, path/share ACL and mount binding
// checks. Never reveal a bucket URL or move authorization to a public CDN.
func serveRemoteDerivative(c *gin.Context, a derivatives.Artifact) {
	reader, err := derivatives.Default.OpenArtifact(c.Request.Context(), a)
	if err != nil {
		status := http.StatusBadGateway
		if derivatives.ErrorCode(err) == "s3_object_missing" {
			status = http.StatusNotFound
		}
		c.AbortWithStatus(status)
		return
	}
	defer reader.Close()
	c.Header("Content-Type", "image/webp")
	c.Header("ETag", "\""+a.SHA256+"\"")
	http.ServeContent(c.Writer, c.Request, "preview.webp", a.UpdatedAt, reader)
}
