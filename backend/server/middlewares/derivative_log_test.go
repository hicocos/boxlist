package middlewares

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDerivativeTicketNeverReachesRequestLogger(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ProtectDerivativeTicket)
	observed := ""
	r.Use(func(c *gin.Context) {
		observed = c.Request.RequestURI
		c.Next()
		observed += " " + c.Request.URL.String()
	})
	r.GET("/api/fs/derivatives/:id/:profile", func(c *gin.Context) {
		if c.GetString("derivative_ticket") != "private-grant" {
			t.Error("handler lost ticket")
		}
		c.Status(204)
	})
	req := httptest.NewRequest("GET", "/api/fs/derivatives/a/thumb?ticket=private-grant", nil)
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)
	if res.Code != 204 || strings.Contains(observed, "private-grant") || strings.Contains(observed, "ticket=") {
		t.Fatalf("request logger observed ticket: %q", observed)
	}
}
