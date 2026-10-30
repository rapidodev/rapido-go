package httpapi

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/legendary1205/rapido-go/internal/integrationsettings"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/hostmetrics"
	"github.com/legendary1205/rapido-go/internal/resellerapi"
)

const nodeIDContextKey = "node.id"

// requireNodeSecret authenticates POST /api/internal/node-report by bearer
// secret (nodes.report_secret), not an admin JWT - a node identifies
// itself this way instead of via mTLS since that direction (node -> panel)
// is the opposite of the mTLS the node's own control-plane already
// requires (panel -> node, see cmd/node/main.go). See migration 00006's
// doc comment for the full rationale.
func (h *Handler) requireNodeSecret(c *gin.Context) {
	const prefix = "Bearer "
	header := c.GetHeader("Authorization")
	if !strings.HasPrefix(header, prefix) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "Missing node report secret"})
		return
	}
	secret := strings.TrimPrefix(header, prefix)
	node, err := h.store.Queries.GetNodeByReportSecret(c.Request.Context(), pgtype.Text{String: secret, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"detail": "Invalid node report secret"})
		return
	}
	if err != nil {
		// Not a verdict on the secret: a node takes a 401 to mean it was removed
		// from this panel, and a database hiccup must not read as that.
		h.logger.Error("node secret lookup failed", "error", err)
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"detail": "Could not check the node secret; retry"})
		return
	}
	c.Set(nodeIDContextKey, node.ID)
	c.Set(nodeRowContextKey, node)
	c.Next()
}

type nodeReportUserUsage struct {
	Username string `json:"username" binding:"required"`
	Uplink   int64  `json:"uplink"`
	Downlink int64  `json:"downlink"`
}

type nodeReportRequest struct {
	ReportID string                `json:"report_id"`
	Users    []nodeReportUserUsage `json:"users"`
	Host     hostmetrics.Sample    `json:"host"`
	// AgentVersion and ConfigError are absent from an agent older than them,
	// which AgentVersion being empty tells apart from an agent with nothing to
	// complain about.
	AgentVersion string `json:"agent_version"`
	ConfigError  string `json:"config_error"`
}

// handleNodeReport implements POST /api/internal/node-report - the
// receiving half of the node agent's push loop (cmd/node/main.go). Must be
// pointed at by the backend-singleton instance specifically, not a
// load-balanced pool of stateless API replicas: h.hostMetricsTracker's
// rate/percent computation is only correct when exactly one process ever
// calls Derive for a given node - see PreviousTracker's own doc comment.
func (h *Handler) handleNodeReport(c *gin.Context) {
	nodeID := c.MustGet(nodeIDContextKey).(int32)

	var req nodeReportRequest
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<20)
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}

	ctx := c.Request.Context()

	if err := h.recordNodeTraffic(ctx, nodeID, req); err != nil {
		h.logger.Error("node report rejected without committing usage", "error", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "Traffic was not committed; retry the same report"})
		return
	}

	if req.Host.CollectedAt.IsZero() {
		req.Host.CollectedAt = time.Now().UTC()
	}
	derived := h.hostMetricsTracker.Derive(nodeKey(nodeID), req.Host)
	if err := hostmetrics.Store(ctx, h.store.Queries, &nodeID, req.Host, derived, true); err != nil {
		h.logger.Error("node report: store host metric", "error", err)
	}

	if err := h.store.Queries.MarkNodeConnectedIfNotDisabled(ctx, nodeID); err != nil {
		h.logger.Error("node report: mark node connected", "error", err)
	}
	h.recordNodeAgentState(ctx, c, req)

	c.JSON(http.StatusOK, gin.H{"detail": "recorded", "report_id": req.ReportID})
}

// maxNodeMessageLen bounds what an agent can leave on the Nodes page.
const maxNodeMessageLen = 500

// recordNodeAgentState keeps what the agent runs and why it can't run the
// current config, the one thing otherwise known only on the node. Written only
// when it changed: this runs on every report of every node.
func (h *Handler) recordNodeAgentState(ctx context.Context, c *gin.Context, req nodeReportRequest) {
	if req.AgentVersion == "" {
		return
	}
	node, ok := c.Get(nodeRowContextKey)
	if !ok {
		return
	}
	row := node.(generated.Node)
	version := pgtype.Text{String: req.AgentVersion, Valid: true}
	message := pgtype.Text{}
	if req.ConfigError != "" {
		text := req.ConfigError
		if len(text) > maxNodeMessageLen {
			text = strings.ToValidUTF8(text[:maxNodeMessageLen], "")
		}
		message = pgtype.Text{String: text, Valid: true}
	}
	if row.XrayVersion == version && row.Message == message {
		return
	}
	if err := h.store.Queries.UpdateNodeAgentState(ctx, generated.UpdateNodeAgentStateParams{ID: row.ID, XrayVersion: version, Message: message}); err != nil {
		h.logger.Error("node report: store agent state", "error", err)
	}
}

func nodeKey(nodeID int32) string {
	return "node:" + strconv.Itoa(int(nodeID))
}

// recordNodeTraffic acknowledges only an atomic, durable commit. A report ID
// is immutable on retry, so a lost HTTP response never bills traffic twice.
func (h *Handler) recordNodeTraffic(ctx context.Context, nodeID int32, req nodeReportRequest) error {
	if len(req.Users) > 100000 {
		return errors.New("too many usage rows")
	}
	if req.ReportID != "" {
		if _, err := uuid.Parse(req.ReportID); err != nil {
			return err
		}
	}
	usernames := make([]string, 0, len(req.Users))
	byName := make(map[string]nodeReportUserUsage, len(req.Users))
	for _, u := range req.Users {
		if u.Username == "" || u.Uplink < 0 || u.Downlink < 0 || u.Uplink > math.MaxInt64-u.Downlink {
			return errors.New("invalid traffic")
		}
		if _, exists := byName[u.Username]; exists {
			return errors.New("duplicate username")
		}
		byName[u.Username] = u
		usernames = append(usernames, u.Username)
	}
	settings, err := h.store.CachedGetIntegrationSettings(ctx)
	if err != nil {
		return err
	}
	values := integrationsettings.Resolve(settings, h.envDefaults)
	queue := resellerapi.Config{Secret: values.ResellerApiSecret, URL: values.ResellerApiUrl}.CanReportUsage()
	tx, err := h.store.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock_shared(814720033)"); err != nil {
		return err
	}
	q := h.store.Queries.WithTx(tx)
	if req.ReportID != "" {
		result, err := tx.Exec(ctx, "INSERT INTO node_report_receipts(node_id,report_id) VALUES($1,$2) ON CONFLICT DO NOTHING", nodeID, req.ReportID)
		if err != nil {
			return err
		}
		if result.RowsAffected() == 0 {
			return tx.Commit(ctx)
		}
	}
	node, err := q.GetNodeByID(ctx, nodeID)
	if err != nil {
		return err
	}
	factor := node.UsageCoefficient
	if factor <= 0 || math.IsNaN(factor) || math.IsInf(factor, 0) {
		return errors.New("invalid node traffic coefficient")
	}
	rows, err := q.GetUsersByUsernames(ctx, usernames)
	if err != nil {
		return err
	}
	ids := make([]int32, 0, len(rows))
	deltas := make([]int64, 0, len(rows))
	admins := map[int32]int64{}
	var up, down int64
	for _, row := range rows {
		u := byName[row.Username]
		raw := u.Uplink + u.Downlink
		scaled := float64(raw) * factor
		if scaled >= float64(math.MaxInt64) {
			return errors.New("traffic overflow")
		}
		delta := int64(scaled)
		if delta <= 0 {
			continue
		}
		if up > math.MaxInt64-u.Uplink || down > math.MaxInt64-u.Downlink {
			return errors.New("traffic overflow")
		}
		ids = append(ids, row.ID)
		deltas = append(deltas, delta)
		up += u.Uplink
		down += u.Downlink
		if row.AdminID.Valid {
			if admins[row.AdminID.Int32] > math.MaxInt64-delta {
				return errors.New("admin traffic overflow")
			}
			admins[row.AdminID.Int32] += delta
		}
	}
	bucket := pgtype.Timestamptz{Time: time.Now().UTC().Truncate(time.Hour), Valid: true}
	if len(ids) > 0 {
		// Keeps BulkIncrementUserUsage locking users in id order; see its comment.
		if _, err := tx.Exec(ctx, "SET LOCAL enable_hashjoin = off"); err != nil {
			return err
		}
		updated, err := q.BulkIncrementUserUsage(ctx, generated.BulkIncrementUserUsageParams{Ids: ids, Deltas: deltas})
		if err != nil {
			return err
		}
		// A user deleted since they were read would fail the whole report on the
		// foreign key here. Those still counted are locked until this commits.
		ids, deltas = onlyThese(updated, ids, deltas)
		if err := q.BulkUpsertNodeUserUsage(ctx, generated.BulkUpsertNodeUserUsageParams{CreatedAt: bucket, NodeID: nodeID, UserIds: ids, Deltas: deltas}); err != nil {
			return err
		}
	}
	if len(admins) > 0 {
		adminIDs := make([]int32, 0, len(admins))
		for id := range admins {
			adminIDs = append(adminIDs, id)
		}
		slices.Sort(adminIDs)
		values := make([]int64, len(adminIDs))
		for i, id := range adminIDs {
			values[i] = admins[id]
		}
		if err := q.BulkIncrementAdminUsage(ctx, generated.BulkIncrementAdminUsageParams{Ids: adminIDs, Deltas: values, QueueForResellerApi: queue}); err != nil {
			return err
		}
	}
	if up > 0 || down > 0 {
		if err := q.UpsertNodeUsage(ctx, generated.UpsertNodeUsageParams{CreatedAt: bucket, NodeID: pgInt4FromInt(int(nodeID)), Uplink: pgInt8FromInt64(up), Downlink: pgInt8FromInt64(down)}); err != nil {
			return err
		}
		if err := q.IncrementNodeCumulative(ctx, generated.IncrementNodeCumulativeParams{ID: nodeID, Uplink: pgInt8FromInt64(up), Downlink: pgInt8FromInt64(down)}); err != nil {
			return err
		}
		if err := q.IncrementSystemCumulative(ctx, generated.IncrementSystemCumulativeParams{Uplink: pgInt8FromInt64(up), Downlink: pgInt8FromInt64(down)}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// onlyThese narrows ids and their deltas to the ids in keep.
func onlyThese(keep, ids []int32, deltas []int64) ([]int32, []int64) {
	if len(keep) == len(ids) {
		return ids, deltas
	}
	wanted := make(map[int32]bool, len(keep))
	for _, id := range keep {
		wanted[id] = true
	}
	keptIDs := make([]int32, 0, len(keep))
	keptDeltas := make([]int64, 0, len(keep))
	for i, id := range ids {
		if wanted[id] {
			keptIDs = append(keptIDs, id)
			keptDeltas = append(keptDeltas, deltas[i])
		}
	}
	return keptIDs, keptDeltas
}
