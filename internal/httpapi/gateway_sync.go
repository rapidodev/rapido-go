package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/proxysettings"
)

// gatewaySyncPayload is what POST /api/internal/gateway/users/sync
// receives - a full snapshot of one user's identity/policy/credentials,
// not a diff, pushed by the panel that actually owns the user. Only what
// a peer needs to authenticate and enforce policy for this user on its
// OWN nodes: no note, no on_hold_*/auto_delete_in_days, no next_plan -
// those are local-panel-only admin concerns this feature has no reason to
// mirror. Field-for-field the same shape as gatewayclient.UserSyncPayload
// (that package can't import this one - see its own doc comment on why
// the two aren't literally the same Go type).
type gatewaySyncPayload struct {
	OriginPanelName        string                     `json:"origin_panel_name"`
	OriginID               string                     `json:"origin_id"`
	Username               string                     `json:"username" binding:"required"`
	Deleted                bool                       `json:"deleted"`
	Status                 string                     `json:"status"`
	DataLimit              *int64                     `json:"data_limit"`
	DataLimitResetStrategy string                     `json:"data_limit_reset_strategy"`
	Expire                 *int64                     `json:"expire"`
	Proxies                map[string]json.RawMessage `json:"proxies"`
}

// handleGatewaySyncUser implements POST /api/internal/gateway/users/sync
// (panel-to-panel, requireGatewaySecret). Upserts a REPLICA by username -
// never touches a genuine local user (one with synced_from_panel_name
// still NULL): a same-username collision is a real admin misconfiguration
// (both panels independently have their own real user with this name) and
// is reported as a conflict, not silently resolved either way.
func (h *Handler) handleGatewaySyncUser(c *gin.Context) {
	var req gatewaySyncPayload
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"ok": false, "detail": err.Error()})
		return
	}
	// A shared secret is all this call asks for, so what it writes is held to what an
	// admin's own create would be.
	if !validUsername(req.Username) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"ok": false, "detail": "invalid username"})
		return
	}
	if !req.Deleted && !gatewaySyncableStatus[req.Status] {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"ok": false, "detail": "invalid status " + req.Status})
		return
	}
	ctx := c.Request.Context()

	existing, err := h.store.Queries.GetUserByUsername(ctx, req.Username)
	found := true
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "detail": err.Error()})
			return
		}
		found = false
	}

	if req.Deleted {
		if !found {
			c.JSON(http.StatusOK, gin.H{"ok": true}) // already gone - idempotent
			return
		}
		if !existing.SyncedFromPanelName.Valid {
			c.JSON(http.StatusConflict, gin.H{"ok": false, "detail": "a real local user with this username exists here - refusing to delete it"})
			return
		}
		if !gatewayOriginOwns(existing, req) {
			c.JSON(http.StatusConflict, gin.H{"ok": false, "detail": "this username is a copy of another panel's user here"})
			return
		}
		if err := h.store.Queries.DeleteUser(ctx, existing.ID); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "detail": "could not delete replica: " + err.Error()})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
		return
	}

	if found && !existing.SyncedFromPanelName.Valid {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "detail": "a real local user with this username already exists here - not a replica, refusing to overwrite it"})
		return
	}
	if found && !gatewayOriginOwns(existing, req) {
		c.JSON(http.StatusConflict, gin.H{"ok": false, "detail": "this username is a copy of another panel's user here"})
		return
	}

	// A Gateway push really is "this replica's complete state, not a diff"
	// (see gatewaySyncPayload's own doc comment), so deleteOmitted=true below
	// deletes every protocol this payload doesn't mention.
	settingsByType, err := storedProxySettings(req.Proxies)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"ok": false, "detail": err.Error()})
		return
	}
	var existingProxies []generated.Proxy
	if found {
		existingProxies, err = h.store.Queries.ListProxiesByUserID(ctx, pgInt4FromInt(int(existing.ID)))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "detail": "could not read proxies"})
			return
		}
	}
	if !h.credentialsAreFree(c, existing.ID, req.Proxies, settingsByType, existingProxies) {
		return
	}
	originID := pgtype.Text{String: req.OriginID, Valid: req.OriginID != ""}

	var userRow generated.User
	if found {
		userRow, err = h.store.Queries.UpdateGatewaySyncedUser(ctx, generated.UpdateGatewaySyncedUserParams{
			ID: existing.ID, Status: req.Status, DataLimit: int8FromPtr(req.DataLimit),
			DataLimitResetStrategy: strOr(&req.DataLimitResetStrategy, "no_reset"),
			Expire:                 pgInt4FromPtrInt64(req.Expire),
			SyncedOriginID:         originID,
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "detail": "could not update replica: " + err.Error()})
			return
		}
		if err := h.reconcileProxies(ctx, userRow.ID, settingsByType, nil, true); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "detail": err.Error()})
			return
		}
	} else {
		userRow, err = h.store.Queries.CreateGatewaySyncedUser(ctx, generated.CreateGatewaySyncedUserParams{
			Username: req.Username, Status: req.Status, DataLimit: int8FromPtr(req.DataLimit),
			DataLimitResetStrategy: strOr(&req.DataLimitResetStrategy, "no_reset"),
			Expire:                 pgInt4FromPtrInt64(req.Expire),
			SyncedFromPanelName:    pgtype.Text{String: gatewayOriginLabel(req), Valid: true},
			SyncedOriginID:         originID,
		})
		if err != nil {
			if isUniqueViolation(err) {
				c.JSON(http.StatusConflict, gin.H{"ok": false, "detail": "a real local user with this username already exists here"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "detail": "could not create replica: " + err.Error()})
			return
		}
		// A brand-new replica defaults to full access on every one of THIS
		// panel's own inbounds for each synced protocol, same as a real new
		// user - this panel's own admin can restrict it locally afterward
		// exactly like any other user, see createProxyForUser's own doc
		// comment.
		for protocol, settings := range settingsByType {
			known, err := h.store.CachedListInboundTagsByProtocol(ctx, protocol)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "detail": err.Error()})
				return
			}
			if err := h.createProxyForUser(ctx, userRow.ID, protocol, settings, known); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"ok": false, "detail": err.Error()})
				return
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{"ok": true, "used_traffic": userRow.UsedTraffic})
}

// gatewaySyncableStatus is every status a user can be in, as the origin decides them.
var gatewaySyncableStatus = map[string]bool{
	statusActive: true, statusDisabled: true, statusLimited: true, statusExpired: true, statusOnHold: true,
}

// gatewayOriginOwns reports whether this push comes from the panel the copy belongs to.
// A copy made before origins had an ID goes by its name, and takes the ID it first
// hears, so it can't be claimed by a panel merely named the same afterwards. One with
// no name, from a panel nobody had named, has nothing to go by and goes to the first.
func gatewayOriginOwns(existing generated.User, req gatewaySyncPayload) bool {
	if existing.SyncedOriginID.Valid {
		return existing.SyncedOriginID.String == req.OriginID
	}
	return existing.SyncedFromPanelName.String == "" || existing.SyncedFromPanelName.String == gatewayOriginLabel(req)
}

// gatewayOriginLabel is what an admin here sees a copy came from. A panel doesn't have
// to be named, and a copy with no label would read as a real local user.
func gatewayOriginLabel(req gatewaySyncPayload) string {
	if req.OriginPanelName != "" {
		return req.OriginPanelName
	}
	if len(req.OriginID) >= 8 {
		return "panel-" + req.OriginID[:8]
	}
	return "another panel"
}

// storedProxySettings reads credentials as a panel stores them, not as a client's
// request: to a request an absent flow means the default, which would turn the origin's
// plain VLESS user into a Vision one here and the core would refuse their connection.
func storedProxySettings(raw map[string]json.RawMessage) (map[string]proxysettings.Settings, error) {
	out := make(map[string]proxysettings.Settings, len(raw))
	for protocol, payload := range raw {
		if !proxysettings.ProxyType(protocol).Valid() {
			return nil, fmt.Errorf("unknown proxy type: %s", protocol)
		}
		settings, err := proxysettings.FromStored(proxysettings.ProxyType(protocol), payload)
		if err != nil {
			return nil, err
		}
		out[protocol] = settings
	}
	return out, nil
}

// handleGatewayUsage implements GET /api/internal/gateway/usage (panel-to-panel,
// requireGatewaySecret): what this panel has counted for one origin's users. They are
// the origin's to add up, so nothing here changes by being read and nothing can be lost
// by a response that doesn't arrive.
func (h *Handler) handleGatewayUsage(c *gin.Context) {
	origin := c.Query("origin")
	if origin == "" {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "origin is required"})
		return
	}
	after, err := strconv.ParseInt(c.DefaultQuery("after", "0"), 10, 32)
	if err != nil || after < 0 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid after"})
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "1000"))
	if err != nil || limit < 1 || limit > 5000 {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "invalid limit"})
		return
	}
	rows, err := h.store.Queries.ListGatewayReplicaUsage(c.Request.Context(), generated.ListGatewayReplicaUsageParams{
		OriginID: pgtype.Text{String: origin, Valid: true}, AfterID: int32(after), RowLimit: int32(limit),
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not read usage"})
		return
	}
	users := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		users = append(users, gin.H{"id": r.ID, "username": r.Username, "used_traffic": r.UsedTraffic})
	}
	c.JSON(http.StatusOK, gin.H{"users": users})
}

// handleGatewayPurge implements POST /api/internal/gateway/users/purge (panel-to-panel,
// requireGatewaySecret): an origin that removed this panel takes its copies with it, or
// they would go on working with nothing limiting them.
func (h *Handler) handleGatewayPurge(c *gin.Context) {
	var req struct {
		OriginID string `json:"origin_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	deleted, err := h.store.Queries.PurgeGatewayReplicas(c.Request.Context(), pgtype.Text{String: req.OriginID, Valid: true})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "could not delete the copies"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"deleted": deleted})
}
