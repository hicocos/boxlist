package handles

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/OpenListTeam/OpenList/v4/internal/derivatives"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestDerivativeCleanupAssessmentHTTP(t *testing.T) {
	t.Setenv("DERIVATIVES_ENABLED", "true")
	database, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "cleanup.sqlite")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "derivatives")
	manager, err := derivatives.New(database, root)
	if err != nil {
		t.Fatal(err)
	}
	old := derivatives.Default
	derivatives.Default = manager
	t.Cleanup(func() {
		derivatives.Default = old
		manager.Close()
		sql, err := database.DB()
		if err == nil {
			sql.Close()
		}
	})
	id := strings.Repeat("a", 64)
	file := "artifacts/aa/" + id + ".webp"
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, file)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, file), []byte("preview"), 0600); err != nil {
		t.Fatal(err)
	}
	artifact := derivatives.Artifact{ID: id, State: "obsolete", File: file, Bytes: 7}
	if err := database.Create(&artifact).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.GET("/cleanup", DerivativeCleanupAssessment)
	router.POST("/cleanup", DerivativeCleanup)
	request := func(method, body string) struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	} {
		t.Helper()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/cleanup", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		var response struct {
			Code int             `json:"code"`
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		return response
	}
	response := request(http.MethodGet, "")
	var assessment derivatives.CleanupAssessment
	if err := json.Unmarshal(response.Data, &assessment); err != nil || response.Code != 200 || assessment.Stale != (derivatives.CleanupTotals{Files: 1, Bytes: 7}) {
		t.Fatal("wrong GET response", response.Code, assessment, err)
	}
	if _, err := os.Stat(filepath.Join(root, file)); err != nil {
		t.Fatal("GET removed preview", err)
	}
	if err := database.First(&artifact, "id = ?", id).Error; err != nil || artifact.State != "obsolete" {
		t.Fatal("GET changed metadata", artifact.State, err)
	}
	if response := request(http.MethodPost, `{}`); response.Code != 400 {
		t.Fatal("POST no longer requires explicit scope", response.Code)
	}
	response = request(http.MethodPost, `{"mode":"stale"}`)
	var result derivatives.CleanupResult
	if err := json.Unmarshal(response.Data, &result); err != nil || response.Code != 200 || result.Files != 1 || result.Bytes != 7 || result.Mode != "obsolete" {
		t.Fatal("POST contract changed", response.Code, result, err)
	}
	if err := os.Remove(filepath.Join(root, "work")); err != nil {
		t.Fatal(err)
	}
	response = request(http.MethodGet, "")
	if response.Code == 200 || (len(response.Data) != 0 && string(response.Data) != "null") {
		t.Fatal("failed GET returned success/partial totals", response.Code, string(response.Data))
	}
}
