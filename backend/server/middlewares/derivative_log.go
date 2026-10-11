package middlewares

import (
	"github.com/gin-gonic/gin"
	"strings"
)

// Place before request loggers. The token remains available to its handler via
// Gin context but is removed from the URL used by access/panic logging.
func ProtectDerivativeTicket(c *gin.Context) {
	if strings.Contains(c.Request.URL.Path, "/api/fs/derivatives/") {
		q := c.Request.URL.Query()
		if token := q.Get("ticket"); token != "" {
			c.Set("derivative_ticket", token)
			q.Del("ticket")
			c.Request.URL.RawQuery = q.Encode()
			c.Request.RequestURI = c.Request.URL.RequestURI()
		}
	}
	c.Next()
}
