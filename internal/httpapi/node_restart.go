package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/legendary1205/rapido-go/internal/auth"
	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/noderestart"
	"github.com/legendary1205/rapido-go/internal/sshexec"
	"github.com/legendary1205/rapido-go/internal/tunnelwatch"
)

// nodeRestartTimeout bounds a restart: connect, restart, and wait for the agent
// to come back and pull its config.
const nodeRestartTimeout = 90 * time.Second

type nodeRestartRequest struct {
	// Mode is "service" (restart the node agent) or "reboot" (the whole server).
	Mode string `json:"mode"`
	// ConfirmName must equal the node's name exactly for mode "reboot".
	ConfirmName string `json:"confirm_name"`
	// A login for this call only, needed when no tunnel row stores one for the
	// node (see ssh_known on GET /api/nodes). Never stored.
	SSHPort     int32  `json:"ssh_port"`
	SSHUser     string `json:"ssh_user"`
	SSHPassword string `json:"ssh_password"`
}

type nodeRestartResponse struct {
	noderestart.Result
	// CredentialsFrom is "tunnel" (the login stored on a tunnel row) or
	// "request" (the one in the body).
	CredentialsFrom string `json:"credentials_from"`
}

// resolveNodeLogin finds the login to use for a node.
func (h *Handler) resolveNodeLogin(ctx context.Context, nodeID int32, req nodeRestartRequest) (noderestart.Login, string, error) {
	var stored *noderestart.Login
	if row, err := h.store.Queries.GetNodeSSHLogin(ctx, nodeID); err == nil {
		stored = &noderestart.Login{Port: row.NodeSshPort, User: row.NodeSshUser, Password: row.NodeSshPassword}
	}
	var supplied *noderestart.Login
	if req.SSHUser != "" || req.SSHPassword != "" {
		supplied = &noderestart.Login{Port: req.SSHPort, User: req.SSHUser, Password: req.SSHPassword}
	}
	return noderestart.ResolveLogin(stored, supplied)
}

// handleRestartNode implements POST /api/node/:id/restart (sudo only). Nodes
// poll the panel and there is no channel back, so this goes in over SSH:
// mode "service" restarts the node agent (rapido-go-node) and waits until it is
// active and has pulled its config again; mode "reboot" reboots the whole
// server - it needs confirm_name equal to the node's name and returns at once.
// WireGuard interfaces, HAProxy and frpc are separate services and are not touched
// by a service restart.
func (h *Handler) handleRestartNode(c *gin.Context) {
	id, err := parseIDParam(c, "id")
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid node id"})
		return
	}
	ctx := c.Request.Context()
	node, err := h.store.Queries.GetNodeByID(ctx, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"detail": "Node not found"})
		return
	}
	var req nodeRestartRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	if err := noderestart.Validate(node.Name, noderestart.Request{Mode: req.Mode, ConfirmName: req.ConfirmName}); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	login, source, err := h.resolveNodeLogin(ctx, node.ID, req)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	if !noderestart.ValidUser(login.User) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "ssh_user is not a valid user name"})
		return
	}

	// Run on a context of its own: a browser that gives up must not leave a
	// restart half-watched.
	rctx, cancel := context.WithTimeout(context.Background(), nodeRestartTimeout)
	defer cancel()
	client, err := sshexec.Dial(rctx, sshexec.Config{Host: node.Address, Port: login.Port, User: login.User, Password: login.Password})
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"detail": "could not connect to the node over SSH: " + err.Error()})
		return
	}
	defer client.Close()

	identity := auth.CurrentIdentity(c)
	var res noderestart.Result
	switch req.Mode {
	case noderestart.ModeReboot:
		res, _ = noderestart.Reboot(rctx, client, login.User, time.Now)
	default:
		res, _ = noderestart.RestartService(rctx, client, login.User, time.Sleep, time.Now)
	}
	h.logger.Info("node restart", "node_id", node.ID, "node", node.Name, "mode", req.Mode, "ok", res.OK, "by", identity.Username, "credentials", source)
	h.recordNodeRestart(rctx, node, req.Mode, identity.Username, res)
	c.JSON(http.StatusOK, nodeRestartResponse{Result: res, CredentialsFrom: source})
}

// recordNodeRestart puts the restart on the timeline of every tunnel that ends
// at this node, so a gap in a chart or a failover right after has its reason
// next to it.
func (h *Handler) recordNodeRestart(ctx context.Context, node generated.Node, mode, by string, res noderestart.Result) {
	ids, err := h.store.Queries.ListTunnelIDsByNode(ctx, node.ID)
	if err != nil || len(ids) == 0 {
		return
	}
	kind, what := "node_restart", "The node agent was restarted"
	if mode == noderestart.ModeReboot {
		kind, what = "node_reboot", "The server was rebooted"
	}
	detail := what + " from the panel by " + by + " (node " + node.Name + "). " + res.Detail
	for _, id := range ids {
		if _, err := h.store.Queries.InsertTunnelEvent(ctx, generated.InsertTunnelEventParams{
			TunnelID: id, At: timestamptzFromTime(time.Now().UTC()), Kind: kind, Severity: tunnelwatch.SeverityWarning, Detail: detail,
		}); err != nil {
			h.logger.Warn("could not record the node restart", "tunnel_id", id, "error", err)
		}
	}
}
