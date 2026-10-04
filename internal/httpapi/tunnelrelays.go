package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/relayhealth"
)

type tunnelRelayDTO struct {
	ID   int32  `json:"id"`
	Name string `json:"name"`
	Host string `json:"host"`
	Port int32  `json:"port"`

	// Live status, published by the backend singleton's relayhealth.Monitor
	// via Redis (see relayhealth.PublishStatuses) - absent entirely (all
	// three nil) when nothing has been published yet, e.g. a relay added
	// seconds ago that hasn't been probed for the first time.
	Up        *bool      `json:"up"`
	Error     *string    `json:"error"`
	CheckedAt *time.Time `json:"checked_at"`
}

func toTunnelRelayDTO(r generated.TunnelRelay, status relayhealth.Status, known bool) tunnelRelayDTO {
	dto := tunnelRelayDTO{ID: r.ID, Name: r.Name, Host: r.Host, Port: r.Port}
	if !known {
		return dto
	}
	up := status.Up
	dto.Up = &up
	checkedAt := status.CheckedAt
	dto.CheckedAt = &checkedAt
	if status.Error != "" {
		dto.Error = &status.Error
	}
	return dto
}

// handleListTunnelRelays implements GET /api/tunnel-relays (sudo only):
// every external relay (GRE+FRP box or similar) registered for health
// probing - see internal/relayhealth's doc comment for what a probe means -
// plus each one's live up/down status as of the backend singleton's last
// probe round.
func (h *Handler) handleListTunnelRelays(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := h.store.Queries.ListTunnelRelays(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not list tunnel relays"})
		return
	}
	// A Redis read failure degrades to "no live status" for every row
	// rather than failing the whole request - the relay list itself (from
	// Postgres) is still useful on its own, matching
	// gateway_subscription.go's same fail-open treatment of a best-effort
	// cached value.
	statuses, _ := relayhealth.ReadStatuses(ctx, h.store.Cache)
	out := make([]tunnelRelayDTO, 0, len(rows))
	for _, r := range rows {
		status, known := statuses[r.ID]
		out = append(out, toTunnelRelayDTO(r, status, known))
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
	c.JSON(http.StatusOK, toTunnelRelayDTO(created, relayhealth.Status{}, false))
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
