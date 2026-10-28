package httpapi

import (
	"context"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
	"time"
)

func requestBudget() gin.HandlerFunc {
	return func(c *gin.Context) {
		limit := int64(32 << 20)
		duration := 30 * time.Second
		if strings.HasPrefix(c.Request.URL.Path, "/api/settings/backup") {
			limit = 512 << 20
			duration = 10 * time.Minute
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		ctx, cancel := context.WithTimeout(c.Request.Context(), duration)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
