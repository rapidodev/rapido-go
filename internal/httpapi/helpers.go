package httpapi

import (
	"strconv"

	"github.com/gin-gonic/gin"
)

// parseIDParam reads an int32 path parameter, e.g. ":id".
func parseIDParam(c *gin.Context, name string) (int32, error) {
	n, err := strconv.ParseInt(c.Param(name), 10, 32)
	if err != nil {
		return 0, err
	}
	return int32(n), nil
}

// clientIP mirrors app/routers/admin.py's get_client_ip: the first
// X-Forwarded-For entry if present, else Gin's own client-address
// resolution, else the literal "Unknown".
func clientIP(c *gin.Context) string {
	if ip := c.ClientIP(); ip != "" {
		return ip
	}
	return "Unknown"
}

func safeLogPath(path string) string {
	const prefix = "/sub/"
	if len(path) >= len(prefix) && path[:len(prefix)] == prefix {
		tail := path[len(prefix):]
		for i, c := range tail {
			if c == '/' {
				return prefix + "[redacted]" + tail[i:]
			}
		}
		return prefix + "[redacted]"
	}
	return path
}
