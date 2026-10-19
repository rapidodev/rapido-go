//go:build !private

package httpapi

import "github.com/gin-gonic/gin"

// registerExtensionRoutes is the hook for optional, separately built routes.
// The public build has none.
func (h *Handler) registerExtensionRoutes(api *gin.RouterGroup, requireSudo gin.HandlerFunc) {}
