package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/relayhealth"
	"github.com/legendary1205/rapido-go/internal/sshexec"
	"github.com/legendary1205/rapido-go/internal/tunneldiscover"
	"github.com/legendary1205/rapido-go/internal/tunnelmetrics"
	"github.com/legendary1205/rapido-go/internal/tunnelprovision"
)

// Tunnels the panel did not build. An admin often sets a tunnel up by hand;
// the panel finds out what it is by reading both ends (internal/tunneldiscover,
// read-only) and from then on only monitors it. Nothing here ever changes,
// restarts or removes anything on those servers.

type discoverRequest struct {
	NodeID          int32  `json:"node_id" binding:"required"`
	NodeSSHPort     int32  `json:"node_ssh_port"`
	NodeSSHUser     string `json:"node_ssh_user" binding:"required"`
	NodeSSHPassword string `json:"node_ssh_password" binding:"required"`

	// The relay's login is optional for discovery: the node alone reveals the
	// protocol and the relay's address. With it the relay is read too.
	RelayHost        string `json:"relay_host"`
	RelaySSHPort     int32  `json:"relay_ssh_port"`
	RelaySSHUser     string `json:"relay_ssh_user"`
	RelaySSHPassword string `json:"relay_ssh_password"`
}

type discoverResponse struct {
	Found        bool     `json:"found"`
	Method       string   `json:"method"`
	Protocol     string   `json:"protocol"`
	RelayHost    *string  `json:"relay_host"`
	RelayAddrs   []string `json:"relay_addrs"`
	ControlPort  int32    `json:"control_port"`
	Ports        []int32  `json:"ports"`
	Interface    string   `json:"interface"`
	HAProxy      bool     `json:"haproxy"`
	FRPTransport *string  `json:"frp_transport"`
	NodeNIC      *string  `json:"node_nic"`
	RelayNIC     *string  `json:"relay_nic"`
	RelayRead    bool     `json:"relay_read"`
	Notes        []string `json:"notes"`
}

func (r discoverRequest) hasRelayLogin() bool {
	return r.RelayHost != "" && r.RelaySSHUser != "" && r.RelaySSHPassword != ""
}

func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// runDiscovery dials the node (and the relay when its login is given) and
// returns the verdict. relayRead says whether the relay was actually read.
func (h *Handler) runDiscovery(ctx context.Context, req discoverRequest) (res tunneldiscover.Result, relayRead bool, status int, err error) {
	node, err := h.store.Queries.GetNodeByID(ctx, req.NodeID)
	if err != nil {
		return res, false, http.StatusUnprocessableEntity, errors.New("unknown node_id")
	}
	nodeClient, err := sshexec.Dial(ctx, sshexec.Config{Host: node.Address, Port: req.NodeSSHPort, User: req.NodeSSHUser, Password: req.NodeSSHPassword})
	if err != nil {
		return res, false, http.StatusBadGateway, errors.New("could not connect to the node: " + err.Error())
	}
	defer nodeClient.Close()

	var relayClient *sshexec.Client
	if req.hasRelayLogin() {
		relayClient, err = sshexec.Dial(ctx, sshexec.Config{Host: req.RelayHost, Port: req.RelaySSHPort, User: req.RelaySSHUser, Password: req.RelaySSHPassword})
		if err != nil {
			return res, false, http.StatusBadGateway, errors.New("could not connect to the relay: " + err.Error())
		}
		defer relayClient.Close()
	}
	res, err = tunneldiscover.Discover(ctx, nodeClient, relayClient)
	if err != nil {
		return res, false, http.StatusBadGateway, errors.New("could not read the servers: " + err.Error())
	}
	return res, relayClient != nil, http.StatusOK, nil
}

// handleDiscoverTunnel implements POST /api/tunnels/discover (sudo only):
// read-only, writes nothing to the database or to either server.
func (h *Handler) handleDiscoverTunnel(c *gin.Context) {
	var req discoverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	res, relayRead, status, err := h.runDiscovery(ctx, req)
	if err != nil {
		c.JSON(status, gin.H{"detail": err.Error()})
		return
	}
	resp := discoverResponse{
		Found: res.Found, Method: res.Method, Protocol: res.Details.Protocol,
		RelayHost: nonEmptyPtr(res.RelayHost), RelayAddrs: orEmptyStrings(res.Details.RelayAddrs),
		ControlPort: res.ControlPort, Ports: orEmptyPorts(res.Ports), Interface: res.Interface,
		HAProxy: res.Details.HAProxy != nil, NodeNIC: nonEmptyPtr(res.Details.NodeNIC), RelayNIC: nonEmptyPtr(res.Details.RelayNIC),
		RelayRead: relayRead, Notes: orEmptyStrings(res.Details.Notes),
	}
	if res.Details.FRP != nil {
		resp.FRPTransport = nonEmptyPtr(res.Details.FRP.Transport)
	}
	if !res.Found {
		resp.Notes = append(resp.Notes, "no GRE, WireGuard or frp tunnel was found on this node")
	}
	c.JSON(http.StatusOK, resp)
}

func orEmptyStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func orEmptyPorts(p []int32) []int32 {
	if p == nil {
		return []int32{}
	}
	return p
}

type adoptTunnelRequest struct {
	Name             string `json:"name" binding:"required"`
	NodeID           int32  `json:"node_id" binding:"required"`
	NodeSSHPort      int32  `json:"node_ssh_port"`
	NodeSSHUser      string `json:"node_ssh_user" binding:"required"`
	NodeSSHPassword  string `json:"node_ssh_password" binding:"required"`
	RelayHost        string `json:"relay_host" binding:"required"`
	RelaySSHPort     int32  `json:"relay_ssh_port"`
	RelaySSHUser     string `json:"relay_ssh_user" binding:"required"`
	RelaySSHPassword string `json:"relay_ssh_password" binding:"required"`
}

// handleAdoptTunnel implements POST /api/tunnels/adopt (sudo only): runs the
// detection itself (it does not trust a client-supplied verdict), then records
// the tunnel as unmanaged so it is monitored from the next round on.
func (h *Handler) handleAdoptTunnel(c *gin.Context) {
	var req adoptTunnelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	if req.NodeSSHPort == 0 {
		req.NodeSSHPort = 22
	}
	if req.RelaySSHPort == 0 {
		req.RelaySSHPort = 22
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()

	res, _, status, err := h.runDiscovery(ctx, discoverRequest{
		NodeID: req.NodeID, NodeSSHPort: req.NodeSSHPort, NodeSSHUser: req.NodeSSHUser, NodeSSHPassword: req.NodeSSHPassword,
		RelayHost: req.RelayHost, RelaySSHPort: req.RelaySSHPort, RelaySSHUser: req.RelaySSHUser, RelaySSHPassword: req.RelaySSHPassword,
	})
	if err != nil {
		c.JSON(status, gin.H{"detail": err.Error()})
		return
	}
	if !res.Found {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "no tunnel was detected on this node"})
		return
	}
	if len(res.Ports) == 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "a tunnel was detected but no forwarded ports could be read from it; check the relay login"})
		return
	}
	method := res.Method
	if method == "" {
		method = tunneldiscover.MethodExternal
	}

	existing, err := h.store.Queries.ListTunnels(ctx)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not list tunnels"})
		return
	}
	for _, t := range existing {
		if t.NodeID == req.NodeID && t.RelayHost == req.RelayHost {
			c.JSON(http.StatusConflict, gin.H{"detail": "this node and relay are already registered as tunnel \"" + t.Name + "\""})
			return
		}
	}
	ifaceName, err := allocateInterfaceName(ctx, h.store.Queries)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not allocate an interface name"})
		return
	}
	details, _ := json.Marshal(res.Details)
	created, err := h.store.Queries.CreateTunnel(ctx, generated.CreateTunnelParams{
		Name: req.Name, Method: method, NodeID: req.NodeID,
		NodeSshPort: req.NodeSSHPort, NodeSshUser: req.NodeSSHUser, NodeSshPassword: req.NodeSSHPassword,
		RelayHost: req.RelayHost, RelaySshPort: req.RelaySSHPort, RelaySshUser: req.RelaySSHUser, RelaySshPassword: req.RelaySSHPassword,
		Ports: res.Ports, InterfaceName: ifaceName,
		// The columns the panel's own provisioning fills in have no meaning for
		// a tunnel it did not build; these are inert placeholders.
		TunnelSubnet: netip.MustParsePrefix("0.0.0.0/32"), FrpControlPort: res.ControlPort, FrpToken: "",
		Status: "active", Managed: false, Details: details,
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not record the tunnel"})
		return
	}
	h.ensureMonitoringLink(ctx, created)
	created, _ = h.store.Queries.GetTunnel(ctx, created.ID)
	c.JSON(http.StatusOK, toTunnelDTO(created, relayhealth.Status{}, false, tunnelmetrics.Metrics{}, false))
}

type patchTunnelRequest struct {
	Name             *string  `json:"name"`
	NodeSSHPort      *int32   `json:"node_ssh_port"`
	NodeSSHUser      *string  `json:"node_ssh_user"`
	NodeSSHPassword  *string  `json:"node_ssh_password"`
	RelayHost        *string  `json:"relay_host"`
	RelaySSHPort     *int32   `json:"relay_ssh_port"`
	RelaySSHUser     *string  `json:"relay_ssh_user"`
	RelaySSHPassword *string  `json:"relay_ssh_password"`
	Ports            *[]int32 `json:"ports"`
}

func normalizePorts(in []int32) ([]int32, error) {
	seen := map[int32]bool{}
	out := make([]int32, 0, len(in))
	for _, p := range in {
		if p <= 0 || p > 65535 {
			return nil, errors.New("every port must be between 1 and 65535")
		}
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("at least one port is required")
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func samePorts(a, b []int32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// handlePatchTunnel implements PATCH /api/tunnels/:id (sudo only): edits the
// name, the stored logins, the relay host and the port list of ANY tunnel.
// An empty or missing password keeps the stored one. For a panel-built tunnel
// a changed port list is applied to the live tunnel in the background (the
// tunnel passes through "starting"); for a detected one the ports only tell
// the monitoring what to watch.
func (h *Handler) handlePatchTunnel(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 32)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "Invalid id"})
		return
	}
	var req patchTunnelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	ctx := c.Request.Context()
	t, err := h.store.Queries.GetTunnel(ctx, int32(id))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Tunnel not found"})
		return
	}
	switch t.Status {
	case "pending", "starting", "stopping", "deleting":
		c.JSON(http.StatusConflict, gin.H{"detail": "the tunnel is busy (" + t.Status + "); try again in a moment"})
		return
	}

	p := generated.UpdateTunnelEditParams{
		ID: t.ID, Name: t.Name,
		NodeSshPort: t.NodeSshPort, NodeSshUser: t.NodeSshUser, NodeSshPassword: t.NodeSshPassword,
		RelayHost: t.RelayHost, RelaySshPort: t.RelaySshPort, RelaySshUser: t.RelaySshUser, RelaySshPassword: t.RelaySshPassword,
		Ports: t.Ports, Details: t.Details,
	}
	if req.Name != nil {
		if name := strings.TrimSpace(*req.Name); name != "" {
			p.Name = name
		} else {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "the name cannot be empty"})
			return
		}
	}
	if req.NodeSSHPort != nil && *req.NodeSSHPort > 0 {
		p.NodeSshPort = *req.NodeSSHPort
	}
	if req.NodeSSHUser != nil && strings.TrimSpace(*req.NodeSSHUser) != "" {
		p.NodeSshUser = strings.TrimSpace(*req.NodeSSHUser)
	}
	if req.NodeSSHPassword != nil && *req.NodeSSHPassword != "" {
		p.NodeSshPassword = *req.NodeSSHPassword
	}
	if req.RelayHost != nil && strings.TrimSpace(*req.RelayHost) != "" {
		p.RelayHost = strings.TrimSpace(*req.RelayHost)
	}
	if req.RelaySSHPort != nil && *req.RelaySSHPort > 0 {
		p.RelaySshPort = *req.RelaySSHPort
	}
	if req.RelaySSHUser != nil && strings.TrimSpace(*req.RelaySSHUser) != "" {
		p.RelaySshUser = strings.TrimSpace(*req.RelaySSHUser)
	}
	if req.RelaySSHPassword != nil && *req.RelaySSHPassword != "" {
		p.RelaySshPassword = *req.RelaySSHPassword
	}
	portsChanged := false
	if req.Ports != nil {
		ports, err := normalizePorts(*req.Ports)
		if err != nil {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
			return
		}
		old, _ := normalizePorts(t.Ports)
		portsChanged = !samePorts(old, ports)
		p.Ports = ports
	}

	updated, err := h.store.Queries.UpdateTunnelEdit(ctx, p)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not save the changes"})
		return
	}
	// The monitoring probe dials the first forwarded port; keep it pointing at
	// a port that still exists, and at the relay's current address.
	if (portsChanged || p.RelayHost != t.RelayHost) && updated.TunnelRelayID.Valid {
		if err := h.store.Queries.UpdateTunnelRelayTarget(ctx, generated.UpdateTunnelRelayTargetParams{
			ID: updated.TunnelRelayID.Int32, Name: updated.Name, Host: updated.RelayHost, Port: updated.Ports[0],
		}); err != nil {
			h.logger.Warn("could not retarget the tunnel's monitoring row", "tunnel_id", t.ID, "error", err)
		}
	}

	if updated.Managed && portsChanged {
		if err := h.store.Queries.UpdateTunnelStatus(ctx, generated.UpdateTunnelStatusParams{ID: updated.ID, Status: "starting"}); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not update the tunnel's status"})
			return
		}
		updated.Status = "starting"
		go h.runTunnelActionInBackground(updated, "active", tunnelprovision.UpdatePorts)
	}
	c.JSON(http.StatusOK, toTunnelDTO(updated, relayhealth.Status{}, false, tunnelmetrics.Metrics{}, false))
}
