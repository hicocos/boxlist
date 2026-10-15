package handles

import (
	"errors"
	"github.com/OpenListTeam/OpenList/v4/internal/conf"
	"github.com/OpenListTeam/OpenList/v4/internal/derivatives"
	"github.com/OpenListTeam/OpenList/v4/internal/model"
	"github.com/OpenListTeam/OpenList/v4/server/common"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"strings"
)

// Route middleware already authenticates the token; reread current role and
// password timestamp before every action rather than trusting cached identity.
func DerivativeAdmin(c *gin.Context) {
	user, err := derivativeFreshUser(c)
	if err != nil || !user.IsAdmin() {
		common.ErrorStrResp(c, "Administrator required", 403)
		c.Abort()
		return
	}
	if derivatives.Default == nil {
		common.ErrorStrResp(c, "Preview generation is unavailable; check configuration", 503)
		c.Abort()
		return
	}
	if c.Request.Method != http.MethodPost || c.FullPath() != "/api/admin/derivatives/restore" {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	}
	c.Header("Cache-Control", "no-store")
	c.Next()
}

func derivativeError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	// The manager's errors are stable reason codes; never serialize upstream
	// network errors (which may contain source URLs or driver credentials).
	code := derivatives.ErrorCode(err)
	switch {
	case errors.Is(err, derivatives.ErrInvalidFilterState):
		code = "invalid_derivative_state"
	case errors.Is(err, derivatives.ErrInvalidSearch):
		code = "invalid_derivative_search"
	}
	common.ErrorStrResp(c, code, 400)
	return true
}

func DerivativeCreateJob(c *gin.Context) {
	var req derivatives.SubmitRequest
	if c.ShouldBindJSON(&req) != nil || !validDerivativePath(req.Path) {
		common.ErrorStrResp(c, "Invalid job request", 400)
		return
	}
	if key := c.GetHeader("Idempotency-Key"); key != "" {
		req.IdempotencyKey = key
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	job, err := derivatives.Default.Submit(c.Request.Context(), user.ID, req)
	if !derivativeError(c, err) {
		common.SuccessResp(c, job)
	}
}

func DerivativeJobs(c *gin.Context) {
	p, n := derivativePage(c)
	jobs, total, err := derivatives.Default.JobsFiltered(p, n, c.Query("state"), c.Query("search"))
	if !derivativeError(c, err) {
		common.SuccessResp(c, gin.H{"content": jobs, "total": total})
	}
}
func DerivativeJobsSummary(c *gin.Context) {
	summary, err := derivatives.Default.JobsSummary()
	if !derivativeError(c, err) {
		common.SuccessResp(c, summary)
	}
}
func DerivativeJob(c *gin.Context) {
	job, err := derivatives.Default.Job(c.Param("id"))
	if !derivativeError(c, err) {
		common.SuccessResp(c, job)
	}
}
func DerivativeItems(c *gin.Context) {
	p, n := derivativePage(c)
	items, total, err := derivatives.Default.ItemsFiltered(c.Param("id"), p, n, c.Query("state"), c.Query("search"))
	if !derivativeError(c, err) {
		common.SuccessResp(c, gin.H{"content": items, "total": total})
	}
}
func DerivativeAction(c *gin.Context) {
	if c.Param("action") == "rescan" {
		old, err := derivatives.Default.Job(c.Param("id"))
		if derivativeError(c, err) {
			return
		}
		user, err := derivativeFreshUser(c)
		if derivativeError(c, err) {
			return
		}
		if old.RuleID != 0 {
			job, err := derivatives.Default.ScanRule(c.Request.Context(), user.ID, strconv.FormatUint(uint64(old.RuleID), 10))
			if !derivativeError(c, err) {
				common.SuccessResp(c, job)
			}
			return
		}
		job, err := derivatives.Default.Submit(c.Request.Context(), user.ID, derivatives.SubmitRequest{Path: old.Path, Recursive: old.Recursive, Profiles: strings.Split(old.Profiles, ",")})
		if !derivativeError(c, err) {
			common.SuccessResp(c, job)
		}
		return
	}
	err := derivatives.Default.Action(c.Param("id"), c.Param("action"))
	if derivativeError(c, err) {
		return
	}
	if c.Param("action") == "delete" {
		common.SuccessResp(c)
		return
	}
	job, err := derivatives.Default.Job(c.Param("id"))
	if !derivativeError(c, err) {
		common.SuccessResp(c, job)
	}
}
func DerivativeRules(c *gin.Context) {
	rules, err := derivatives.Default.Rules()
	if !derivativeError(c, err) {
		common.SuccessResp(c, rules)
	}
}
func DerivativeSaveRule(c *gin.Context) {
	var req derivatives.RuleRequest
	if c.ShouldBindJSON(&req) != nil || !validDerivativePath(req.Path) {
		common.ErrorStrResp(c, "Invalid rule request", 400)
		return
	}
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	rule, err := derivatives.Default.SaveRule(c.Request.Context(), user.ID, req)
	if !derivativeError(c, err) {
		common.SuccessResp(c, rule)
	}
}
func DerivativeDeleteRule(c *gin.Context) {
	if !derivativeError(c, derivatives.Default.DeleteRule(c.Param("id"))) {
		common.SuccessResp(c, nil)
	}
}
func DerivativeScanRule(c *gin.Context) {
	user := c.Request.Context().Value(conf.UserKey).(*model.User)
	job, err := derivatives.Default.ScanRule(c.Request.Context(), user.ID, c.Param("id"))
	if !derivativeError(c, err) {
		common.SuccessResp(c, job)
	}
}
func DerivativeStorage(c *gin.Context) { common.SuccessResp(c, derivatives.Default.Storage()) }
func DerivativeCleanupAssessment(c *gin.Context) {
	result, err := derivatives.Default.AssessCleanup(c.Request.Context())
	if !derivativeError(c, err) {
		common.SuccessResp(c, result)
	}
}
func DerivativeVerify(c *gin.Context) {
	result, err := derivatives.Default.Verify(c.Request.Context())
	if !derivativeError(c, err) {
		common.SuccessResp(c, result)
	}
}
func DerivativeCleanup(c *gin.Context) {
	var req struct {
		Mode string `json:"mode"`
	}
	if c.ShouldBindJSON(&req) != nil || (req.Mode != "stale" && req.Mode != "temporary") {
		common.ErrorStrResp(c, "Cleanup requires explicit stale or temporary scope", 400)
		return
	}
	result, err := derivatives.Default.CleanupContext(c.Request.Context(), req.Mode)
	if !derivativeError(c, err) {
		common.SuccessResp(c, result)
	}
}
