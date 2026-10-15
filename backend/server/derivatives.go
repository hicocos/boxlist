package server

import (
	"github.com/OpenListTeam/OpenList/v4/server/handles"
	"github.com/gin-gonic/gin"
)

func derivativeAdminRoutes(g *gin.RouterGroup) {
	g.Use(handles.DerivativeAdmin)
	g.GET("/backup", handles.DerivativeBackup)
	g.POST("/restore", handles.DerivativeRestore)
	g.GET("/jobs", handles.DerivativeJobs)
	g.POST("/jobs", handles.DerivativeCreateJob)
	g.GET("/jobs/summary", handles.DerivativeJobsSummary)
	g.GET("/jobs/:id", handles.DerivativeJob)
	g.GET("/jobs/:id/items", handles.DerivativeItems)
	g.POST("/jobs/:id/:action", handles.DerivativeAction)
	g.GET("/rules", handles.DerivativeRules)
	g.POST("/rules", handles.DerivativeSaveRule)
	g.DELETE("/rules/:id", handles.DerivativeDeleteRule)
	g.POST("/rules/:id/scan", handles.DerivativeScanRule)
	g.GET("/storage", handles.DerivativeStorage)
	g.GET("/workers", handles.DerivativeWorkers)
	g.POST("/workers", handles.DerivativeSaveWorkers)
	g.GET("/s3", handles.DerivativeS3Configuration)
	g.POST("/s3", handles.DerivativeS3Save)
	g.POST("/s3/test", handles.DerivativeS3Test)
	g.GET("/cleanup", handles.DerivativeCleanupAssessment)
	g.POST("/cleanup", handles.DerivativeCleanup)
	g.POST("/verify", handles.DerivativeVerify)
}
