package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/relayhealth"
	"github.com/legendary1205/rapido-go/internal/sshexec"
	"github.com/legendary1205/rapido-go/internal/tunnelmetrics"
	"github.com/legendary1205/rapido-go/internal/tunnelprovision"
)

// A Tunnel row is both the provisioning record (Phase 3) and, merged in
// here, its own monitoring entry: the standalone "Tunnel Relays" page
// (add a bare host:port for health-only probing, with no SSH access) was
// folded into this one page/endpoint - every real relay this fleet has is
// a provisioned tunnel now, and a tunnel already carries everything a
// health/resource check needs (the relay's own SSH credentials), so there
// was no reason left to keep them as two separate concepts.

type tunnelHealthDTO struct {
	Up        bool       `json:"up"`
	Error     string     `json:"error,omitempty"`
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	// Since is when Up last changed; DownForSeconds, present only while
	// down, is how long ago that was - the same number an InfraAlert
	// recovery message would report, shown live on the dashboard without
	// waiting for a recovery to see it.
	Since          *time.Time `json:"since,omitempty"`
	DownForSeconds *float64   `json:"down_for_seconds,omitempty"`
}

type tunnelMetricsDTO struct {
	CPUPercent  float64   `json:"cpu_percent"`
	MemUsedMB   int64     `json:"mem_used_mb"`
	MemTotalMB  int64     `json:"mem_total_mb"`
	DiskUsedGB  float64   `json:"disk_used_gb"`
	DiskTotalGB float64   `json:"disk_total_gb"`
	RxBytes     int64     `json:"rx_bytes"`
	TxBytes     int64     `json:"tx_bytes"`
	// RxDropped/TxDropped/RxErrors/TxErrors are the GRE interface's own
	// cumulative loss counters (since the relay's last reboot) - real
	// packet loss, not an estimate. Connections is a live (not
	// cumulative) count of established TCP sockets on the relay using one
	// of this tunnel's forwarded ports right now - see
	// internal/tunnelmetrics.Metrics's own doc comment for why there is
	// no "total connections ever" figure here.
	RxDropped   int64     `json:"rx_dropped"`
	TxDropped   int64     `json:"tx_dropped"`
	RxErrors    int64     `json:"rx_errors"`
	TxErrors    int64     `json:"tx_errors"`
	Connections int       `json:"connections"`
	CheckedAt   time.Time `json:"checked_at"`
}

type tunnelDTO struct {
	ID             int32   `json:"id"`
	Name           string  `json:"name"`
	Method         string  `json:"method"`
	NodeID         int32   `json:"node_id"`
	RelayHost      string  `json:"relay_host"`
	Ports          []int32 `json:"ports"`
	InterfaceName  string  `json:"interface_name"`
	FRPControlPort int32   `json:"frp_control_port"`
	Status         string  `json:"status"`
	StatusMessage  *string `json:"status_message"`

	// Live health (internal/relayhealth, probed via the linked tunnel_relays
	// row) and resource metrics (internal/tunnelmetrics, probed via this
	// tunnel's own stored SSH credentials) - both nil until the backend
	// singleton's first round publishes something.
	Health  *tunnelHealthDTO  `json:"health"`
	Metrics *tunnelMetricsDTO `json:"metrics"`
}

func toTunnelDTO(t generated.Tunnel, health relayhealth.Status, healthKnown bool, metrics tunnelmetrics.Metrics, metricsKnown bool) tunnelDTO {
	dto := tunnelDTO{
		ID: t.ID, Name: t.Name, Method: t.Method, NodeID: t.NodeID,
		RelayHost: t.RelayHost, Ports: t.Ports, InterfaceName: t.InterfaceName,
		FRPControlPort: t.FrpControlPort, Status: t.Status,
	}
	if t.StatusMessage.Valid {
		dto.StatusMessage = &t.StatusMessage.String
	}
	if healthKnown {
		h := tunnelHealthDTO{Up: health.Up, Error: health.Error, CheckedAt: &health.CheckedAt, Since: &health.Since}
		if !health.Up {
			secs := time.Since(health.Since).Seconds()
			h.DownForSeconds = &secs
		}
		dto.Health = &h
	}
	if metricsKnown {
		m := tunnelMetricsDTO{
			CPUPercent: metrics.CPUPercent, MemUsedMB: metrics.MemUsedMB, MemTotalMB: metrics.MemTotalMB,
			DiskUsedGB: metrics.DiskUsedGB, DiskTotalGB: metrics.DiskTotalGB,
			RxBytes: metrics.RxBytes, TxBytes: metrics.TxBytes, CheckedAt: metrics.CheckedAt,
			RxDropped: metrics.RxDropped, TxDropped: metrics.TxDropped,
			RxErrors: metrics.RxErrors, TxErrors: metrics.TxErrors, Connections: metrics.Connections,
		}
		dto.Metrics = &m
	}
	return dto
}

// handleListTunnels implements GET /api/tunnels (sudo only): every
// provisioned tunnel, merged with its live health (by tunnel_relay_id) and
// resource metrics (by tunnel id). A Redis read failure degrades to "no
// live data" for every row rather than failing the whole request - the
// list itself (from Postgres) is still useful on its own.
func (h *Handler) handleListTunnels(c *gin.Context) {
	ctx := c.Request.Context()
	rows, err := h.store.Queries.ListTunnels(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not list tunnels"})
		return
	}
	healthByRelayID, _ := relayhealth.ReadStatuses(ctx, h.store.Cache)
	metricsByTunnelID, _ := tunnelmetrics.ReadMetrics(ctx, h.store.Cache)

	out := make([]tunnelDTO, 0, len(rows))
	for _, r := range rows {
		health, healthKnown := relayhealth.Status{}, false
		if r.TunnelRelayID.Valid {
			health, healthKnown = healthByRelayID[r.TunnelRelayID.Int32]
		}
		metrics, metricsKnown := metricsByTunnelID[r.ID]
		out = append(out, toTunnelDTO(r, health, healthKnown, metrics, metricsKnown))
	}
	c.JSON(http.StatusOK, out)
}

// allocateTunnelSubnet picks the first /30 in 10.100.0.0/16 not already
// handed to another tunnel (ListUsedTunnelSubnets) - 16384 of them, far
// more than this feature will ever need, so "first free one, in order" is
// simple and sufficient rather than anything cleverer.
func allocateTunnelSubnet(used []netip.Prefix) (netip.Prefix, error) {
	usedSet := make(map[netip.Prefix]bool, len(used))
	for _, u := range used {
		usedSet[u] = true
	}
	base := netip.MustParseAddr("10.100.0.0")
	for i := 0; i < 16384; i++ {
		addr := addOffsetIP(base, i*4)
		p := netip.PrefixFrom(addr, 30)
		if !usedSet[p] {
			return p, nil
		}
	}
	return netip.Prefix{}, errNoSubnetAvailable
}

func addOffsetIP(a netip.Addr, n int) netip.Addr {
	b := a.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v += uint32(n)
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

var errNoSubnetAvailable = &inboundValidationError{"no tunnel subnet available"}

// allocateFRPControlPort picks the first port >= 7000 not already used by
// another tunnel on the SAME relay host - unlike the subnet (globally
// unique, cheap to guarantee and always safe), the control port only needs
// to be unique per physical relay box, since two different boxes listening
// on the same port number don't conflict with each other at all.
//
// Starts at 27000, not frp's own common tutorial default of 7000: every
// relay/node this fleet already had before this feature existed was
// hand-configured using exactly that default, so starting there is a near-
// guaranteed collision with something this allocator has no way to see
// (tunnelprovision.ensurePathFree is the hard backstop that refuses to
// overwrite such a collision either way, but avoiding it in the first
// allocation attempt means a tunnel create doesn't fail for a reason that
// has nothing to do with the request itself).
func allocateFRPControlPort(used []int32) int32 {
	usedSet := make(map[int32]bool, len(used))
	for _, u := range used {
		usedSet[u] = true
	}
	for port := int32(27000); port < 27000+1000; port++ {
		if !usedSet[port] {
			return port
		}
	}
	return 0
}

// allocateInterfaceName returns a short (well under the 15-char IFNAMSIZ-1
// Linux limit - see cmd/node/cli.go's own doc comment on this exact
// constraint), space-free, random interface name, retrying on the
// astronomically unlikely chance it collides with one already in use.
func allocateInterfaceName(ctx context.Context, q interface {
	ListTunnels(ctx context.Context) ([]generated.Tunnel, error)
}) (string, error) {
	existing, err := q.ListTunnels(ctx)
	if err != nil {
		return "", err
	}
	used := make(map[string]bool, len(existing))
	for _, t := range existing {
		used[t.InterfaceName] = true
	}
	for i := 0; i < 20; i++ {
		name, err := generateReportSecret()
		if err != nil {
			return "", err
		}
		name = "t" + name[:6]
		if !used[name] {
			return name, nil
		}
	}
	return "", &inboundValidationError{"could not allocate a free interface name"}
}

type createTunnelRequest struct {
	Name             string  `json:"name" binding:"required"`
	NodeID           int32   `json:"node_id" binding:"required"`
	NodeSSHPort      int32   `json:"node_ssh_port"`
	NodeSSHUser      string  `json:"node_ssh_user" binding:"required"`
	NodeSSHPassword  string  `json:"node_ssh_password" binding:"required"`
	RelayHost        string  `json:"relay_host" binding:"required"`
	RelaySSHPort     int32   `json:"relay_ssh_port"`
	RelaySSHUser     string  `json:"relay_ssh_user" binding:"required"`
	RelaySSHPassword string  `json:"relay_ssh_password" binding:"required"`
	Ports            []int32 `json:"ports" binding:"required"`
}

// handleCreateTunnel implements POST /api/tunnels (sudo only): allocates
// this tunnel's parameters, inserts it as status="pending", and returns
// immediately - provisioning itself (two SSH sessions, a possible frp
// download, several systemd operations) runs in the background and can
// take anywhere from a few seconds to the better part of a minute, too
// long to hold one HTTP request (and the browser/proxy timeouts behind it)
// open for. The dashboard polls GET /api/tunnels to watch status move to
// "active" or "failed".
func (h *Handler) handleCreateTunnel(c *gin.Context) {
	var req createTunnelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	if len(req.Ports) == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "at least one port is required"})
		return
	}
	for _, p := range req.Ports {
		if p <= 0 || p > 65535 {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "every port must be between 1 and 65535"})
			return
		}
	}
	if req.NodeSSHPort == 0 {
		req.NodeSSHPort = 22
	}
	if req.RelaySSHPort == 0 {
		req.RelaySSHPort = 22
	}

	ctx := c.Request.Context()
	node, err := h.store.Queries.GetNodeByID(ctx, req.NodeID)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "unknown node_id"})
		return
	}

	usedSubnets, err := h.store.Queries.ListUsedTunnelSubnets(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not allocate a tunnel subnet"})
		return
	}
	subnet, err := allocateTunnelSubnet(usedSubnets)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}
	usedPorts, err := h.store.Queries.ListUsedFRPControlPorts(ctx, req.RelayHost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not allocate an FRP control port"})
		return
	}
	controlPort := allocateFRPControlPort(usedPorts)
	if controlPort == 0 {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "no FRP control port available on this relay"})
		return
	}
	ifaceName, err := allocateInterfaceName(ctx, h.store.Queries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not allocate an interface name"})
		return
	}
	token, err := generateReportSecret()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not generate an FRP token"})
		return
	}

	created, err := h.store.Queries.CreateTunnel(ctx, generated.CreateTunnelParams{
		Name: req.Name, Method: "gre_frp", NodeID: req.NodeID,
		NodeSshPort: req.NodeSSHPort, NodeSshUser: req.NodeSSHUser, NodeSshPassword: req.NodeSSHPassword,
		RelayHost: req.RelayHost, RelaySshPort: req.RelaySSHPort, RelaySshUser: req.RelaySSHUser, RelaySshPassword: req.RelaySSHPassword,
		Ports: req.Ports, InterfaceName: ifaceName, TunnelSubnet: subnet, FrpControlPort: controlPort, FrpToken: token,
		Status: "pending",
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not create the tunnel"})
		return
	}

	go h.provisionTunnelInBackground(created, node)

	c.JSON(http.StatusOK, toTunnelDTO(created, relayhealth.Status{}, false, tunnelmetrics.Metrics{}, false))
}

// dialTunnelEnds connects to both ends with this tunnel's own stored
// credentials, or fails outright if either is unreachable - unlike
// handleDeleteTunnel's teardown dial (which tolerates one side being gone,
// since deleting a half-dead tunnel must still work), every caller here
// (provision, stop, start, restart) needs both ends to do anything
// meaningful, so there is nothing useful to do with just one.
func (h *Handler) dialTunnelEnds(ctx context.Context, t generated.Tunnel, node generated.Node) (relay, nodeClient *sshexec.Client, err error) {
	relay, err = sshexec.Dial(ctx, sshexec.Config{Host: t.RelayHost, Port: t.RelaySshPort, User: t.RelaySshUser, Password: t.RelaySshPassword})
	if err != nil {
		return nil, nil, fmt.Errorf("could not connect to the relay: %w", err)
	}
	nodeClient, err = sshexec.Dial(ctx, sshexec.Config{Host: node.Address, Port: t.NodeSshPort, User: t.NodeSshUser, Password: t.NodeSshPassword})
	if err != nil {
		relay.Close()
		return nil, nil, fmt.Errorf("could not connect to the node: %w", err)
	}
	return relay, nodeClient, nil
}

func tunnelParams(t generated.Tunnel, nodeAddress string) tunnelprovision.Params {
	return tunnelprovision.Params{
		InterfaceName: t.InterfaceName, NodeAddress: nodeAddress, RelayHost: t.RelayHost,
		TunnelSubnet: t.TunnelSubnet, FRPControlPort: t.FrpControlPort, FRPToken: t.FrpToken, Ports: t.Ports,
	}
}

// provisionTunnelInBackground runs Provision against the two real boxes
// and updates the row's status with the outcome. Uses its own
// context.Background()-derived timeout, not the original request's context
// - that one is cancelled the moment handleCreateTunnel's response is
// written, long before provisioning finishes.
func (h *Handler) provisionTunnelInBackground(t generated.Tunnel, node generated.Node) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	relay, nodeClient, err := h.dialTunnelEnds(ctx, t, node)
	if err != nil {
		h.failTunnel(ctx, t.ID, err.Error())
		return
	}
	defer relay.Close()
	defer nodeClient.Close()

	params := tunnelParams(t, node.Address)
	if err := tunnelprovision.Provision(ctx, relay, nodeClient, params); err != nil {
		h.failTunnel(ctx, t.ID, err.Error())
		// Best-effort cleanup of whatever partially came up, so a failed
		// attempt doesn't leave stray systemd units/processes behind for a
		// retry (a new POST, which allocates a fresh interface name/port
		// rather than reusing this failed row) to collide with.
		tunnelprovision.Teardown(ctx, relay, nodeClient, params)
		return
	}

	h.ensureMonitoringLink(ctx, t)

	if err := h.store.Queries.UpdateTunnelStatus(ctx, generated.UpdateTunnelStatusParams{ID: t.ID, Status: "active"}); err != nil {
		h.logger.Warn("tunnel provisioned but its status could not be updated", "tunnel_id", t.ID, "error", err)
	}
}

// ensureMonitoringLink creates this tunnel's relayhealth monitoring row if
// it doesn't already have one - normally set once at creation, but also
// checked whenever a tunnel settles into "active" (see
// runTunnelActionInBackground), since an admin can delete a monitoring row
// from the Tunnels page without meaning to detach it from a real, still-
// running tunnel - the next Start/Restart re-heals it rather than leaving
// that tunnel's health permanently unmonitored.
func (h *Handler) ensureMonitoringLink(ctx context.Context, t generated.Tunnel) {
	if t.TunnelRelayID.Valid {
		return
	}
	relayRow, err := h.store.Queries.CreateTunnelRelay(ctx, generated.CreateTunnelRelayParams{
		Name: t.Name, Host: t.RelayHost, Port: t.Ports[0],
	})
	if err != nil {
		h.logger.Warn("could not register tunnel's monitoring row", "tunnel_id", t.ID, "error", err)
		return
	}
	if err := h.store.Queries.SetTunnelRelayID(ctx, generated.SetTunnelRelayIDParams{ID: t.ID, TunnelRelayID: pgInt4FromInt(int(relayRow.ID))}); err != nil {
		h.logger.Warn("could not link tunnel to its monitoring row", "tunnel_id", t.ID, "error", err)
	}
}

func (h *Handler) failTunnel(ctx context.Context, id int32, message string) {
	if err := h.store.Queries.UpdateTunnelStatus(ctx, generated.UpdateTunnelStatusParams{
		ID: id, Status: "failed", StatusMessage: textFromPtr(&message),
	}); err != nil {
		h.logger.Warn("could not record tunnel provisioning failure", "tunnel_id", id, "error", err)
	}
}

// tunnelAction is the shape Provision/Teardown/Start/Stop/Restart all
// share - handleTunnelAction runs whichever one is given against a tunnel's
// two real ends in the background, the same way provisionTunnelInBackground
// does for create.
type tunnelAction func(ctx context.Context, relay, node *sshexec.Client, p tunnelprovision.Params) error

// handleTunnelAction implements the Stop/Start/Restart buttons: marks the
// row with a transient status immediately (so the dashboard shows
// something happening right away), runs the action in the background, and
// settles on successStatus or "failed" - the exact async/poll shape
// handleCreateTunnel already established, since these SSH round trips are
// just as unsuited to blocking one HTTP request as provisioning is.
func (h *Handler) handleTunnelAction(c *gin.Context, transientStatus, successStatus string, action tunnelAction) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "Invalid id"})
		return
	}
	ctx := c.Request.Context()
	t, err := h.store.Queries.GetTunnel(ctx, int32(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Tunnel not found"})
		return
	}
	if err := h.store.Queries.UpdateTunnelStatus(ctx, generated.UpdateTunnelStatusParams{ID: t.ID, Status: transientStatus}); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not update the tunnel's status"})
		return
	}
	t.Status = transientStatus

	go h.runTunnelActionInBackground(t, successStatus, action)

	c.JSON(http.StatusOK, toTunnelDTO(t, relayhealth.Status{}, false, tunnelmetrics.Metrics{}, false))
}

func (h *Handler) runTunnelActionInBackground(t generated.Tunnel, successStatus string, action tunnelAction) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	node, err := h.store.Queries.GetNodeByID(ctx, t.NodeID)
	if err != nil {
		h.failTunnel(ctx, t.ID, "could not look up this tunnel's node: "+err.Error())
		return
	}
	relay, nodeClient, err := h.dialTunnelEnds(ctx, t, node)
	if err != nil {
		h.failTunnel(ctx, t.ID, err.Error())
		return
	}
	defer relay.Close()
	defer nodeClient.Close()

	if err := action(ctx, relay, nodeClient, tunnelParams(t, node.Address)); err != nil {
		h.failTunnel(ctx, t.ID, err.Error())
		return
	}
	if successStatus == "active" {
		h.ensureMonitoringLink(ctx, t)
	}
	if err := h.store.Queries.UpdateTunnelStatus(ctx, generated.UpdateTunnelStatusParams{ID: t.ID, Status: successStatus}); err != nil {
		h.logger.Warn("tunnel action succeeded but its status could not be updated", "tunnel_id", t.ID, "error", err)
	}
}

// handleStopTunnel implements POST /api/tunnels/:id/stop: disables and
// stops both ends' GRE+frp units without removing any config, so Start can
// resume with nothing re-provisioned.
func (h *Handler) handleStopTunnel(c *gin.Context) {
	h.handleTunnelAction(c, "stopping", "stopped", tunnelprovision.Stop)
}

// handleStartTunnel implements POST /api/tunnels/:id/start: re-enables what
// Stop turned off, with the same ping-based connectivity check Provision
// uses.
func (h *Handler) handleStartTunnel(c *gin.Context) {
	h.handleTunnelAction(c, "starting", "active", tunnelprovision.Start)
}

// handleRestartTunnel implements POST /api/tunnels/:id/restart: Stop
// immediately followed by Start.
func (h *Handler) handleRestartTunnel(c *gin.Context) {
	h.handleTunnelAction(c, "starting", "active", tunnelprovision.Restart)
}

// handleDeleteTunnel implements DELETE /api/tunnels/:id (sudo only): tears
// down both ends (best-effort - see tunnelprovision.Teardown's own doc
// comment) before removing the row and its monitoring entry, so a relay
// that's unreachable (already decommissioned, say) still lets the admin
// clear the tunnel out of the panel rather than being stuck undeletable.
func (h *Handler) handleDeleteTunnel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "Invalid id"})
		return
	}
	ctx := c.Request.Context()
	t, err := h.store.Queries.GetTunnel(ctx, int32(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Tunnel not found"})
		return
	}
	node, err := h.store.Queries.GetNodeByID(ctx, t.NodeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not look up this tunnel's node"})
		return
	}

	h.store.Queries.UpdateTunnelStatus(ctx, generated.UpdateTunnelStatusParams{ID: t.ID, Status: "deleting"})

	teardownCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var teardownErr string
	relay, relayErr := sshexec.Dial(teardownCtx, sshexec.Config{Host: t.RelayHost, Port: t.RelaySshPort, User: t.RelaySshUser, Password: t.RelaySshPassword})
	if relayErr != nil {
		teardownErr = "relay unreachable: " + relayErr.Error()
	} else {
		defer relay.Close()
	}
	nodeClient, nodeErr := sshexec.Dial(teardownCtx, sshexec.Config{Host: node.Address, Port: t.NodeSshPort, User: t.NodeSshUser, Password: t.NodeSshPassword})
	if nodeErr != nil {
		if teardownErr != "" {
			teardownErr += "; "
		}
		teardownErr += "node unreachable: " + nodeErr.Error()
	} else {
		defer nodeClient.Close()
	}

	if err := tunnelprovision.Teardown(teardownCtx, relay, nodeClient, tunnelParams(t, node.Address)); err != nil {
		h.logger.Warn("tunnel teardown had errors, deleting the row anyway", "tunnel_id", t.ID, "error", err)
	}

	if t.TunnelRelayID.Valid {
		if err := h.store.Queries.DeleteTunnelRelay(ctx, t.TunnelRelayID.Int32); err != nil {
			h.logger.Warn("could not remove tunnel's monitoring row", "tunnel_id", t.ID, "error", err)
		}
	}
	if err := h.store.Queries.DeleteTunnel(ctx, t.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not delete the tunnel"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"detail": "Tunnel deleted", "teardown_warning": teardownErrOrNil(teardownErr)})
}

func teardownErrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
