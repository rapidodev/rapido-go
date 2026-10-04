package httpapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

type tunnelRelayDTO struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int32  `json:"port"`
}

func toTunnelRelayDTO(r generated.TunnelRelay) tunnelRelayDTO {
	return tunnelRelayDTO{ID: r.ID, Name: r.Name, Host: r.Host, Port: r.Port}
}

// handleListTunnelRelays implements GET /api/tunnel-relays (sudo only):
// every external relay (GRE+FRP box or similar) registered for health
// probing - see internal/relayhealth's doc comment for what a probe means.
func (h *Handler) handleListTunnelRelays(c *gin.Context) {
	rows, err := h.store.Queries.ListTunnelRelays(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not list tunnel relays"})
		return
	}
	out := make([]tunnelRelayDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toTunnelRelayDTO(r))
	}
	c.JSON(http.StatusOK, out)
}

// handleCreateTunnelRelay implements POST /api/tunnel-relays (sudo only).
// Port should be one the relay actually forwards into the node (e.g. a
// VLESS location's port) - reaching it is what proves the relay, its
// tunnel to the node, and the node's own listener are all still working,
// not just that the box itself answers SSH.
func (h *Handler) handleCreateTunnelRelay(c *gin.Context) {
	var body struct {
		Name string `json:"name" binding:"required"`
		Host string `json:"host" binding:"required"`
		Port int32  `json:"port" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	created, err := h.store.Queries.CreateTunnelRelay(c.Request.Context(), generated.CreateTunnelRelayParams{
		Name: body.Name, Host: body.Host, Port: body.Port,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not create tunnel relay"})
		return
	}
	c.JSON(http.StatusOK, toTunnelRelayDTO(created))
}

// handleDeleteTunnelRelay implements DELETE /api/tunnel-relays/:id (sudo
// only).
func (h *Handler) handleDeleteTunnelRelay(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "Invalid id"})
		return
	}
	if err := h.store.Queries.DeleteTunnelRelay(c.Request.Context(), int32(id)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not delete tunnel relay"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"detail": "Tunnel relay deleted"})
}
