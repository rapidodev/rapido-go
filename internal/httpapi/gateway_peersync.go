package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/cache"
	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/gatewayclient"
	"github.com/legendary1205/rapido-go/internal/integrationsettings"
	"github.com/legendary1205/rapido-go/internal/resellerapi"
)

const (
	// gatewayPeerCallTimeout bounds one call to a peer, so a hung one can't hold up
	// this peer's pass for long, and never another peer's, which runs beside it.
	gatewayPeerCallTimeout = 10 * time.Second
	// gatewayPeerStatusTTL outlives a few failed passes: a peer's last-known hosts
	// keep being served through a blip, and vanish from subscriptions once it has
	// been unreachable for long enough that they would be stale.
	gatewayPeerStatusTTL = 10 * time.Minute
	// gatewayPushesPerPass bounds a pass, so that during the first push of a whole
	// panel's users the peer's traffic is still read every tick or so.
	gatewayPushesPerPass = 2000
	gatewayPushWorkers   = 8
	gatewayUsagePage     = 1000
)

// SyncGatewayPeers is one pass over every peer, each beside the others so a slow one
// costs nobody else anything:
//
//   - its hosts and load are cached, for subscriptions and the admin's page
//   - the traffic it has counted for this panel's users is added to their limits
//   - any user whose state differs from what it holds is pushed to it
//
// Every step reads the state to act on from the database, so one that fails is simply
// done by a later pass. The pass's outcome is what the admin sees on the Gateway page.
func (h *Handler) SyncGatewayPeers(ctx context.Context) {
	peers, err := h.store.Queries.ListGatewayPeers(ctx)
	if err != nil {
		h.logger.Warn("gateway: could not list peers", "error", err)
		return
	}
	if len(peers) == 0 {
		return
	}
	settings, err := h.ensureGatewaySettings(ctx)
	if err != nil {
		h.logger.Warn("gateway: could not load this panel's settings", "error", err)
		return
	}
	var wg sync.WaitGroup
	for _, peer := range peers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reached, err := h.syncGatewayPeer(ctx, settings, peer)
			problem := ""
			if err != nil {
				problem = err.Error()
				// A peer staying unreachable has to be visible in the logs too, not just
				// quietly missing from subscriptions with no trace of why.
				h.logger.Warn("gateway: peer pass failed", "peer", peer.Name, "error", err)
			}
			if ctx.Err() != nil {
				return
			}
			if err := h.store.Queries.UpdateGatewayPeerHealth(ctx, generated.UpdateGatewayPeerHealthParams{
				ID: peer.ID, Reached: reached, LastError: problem,
			}); err != nil {
				h.logger.Warn("gateway: could not record peer health", "peer", peer.Name, "error", err)
			}
		}()
	}
	wg.Wait()
}

// syncGatewayPeer reports whether the peer answered at all, and the first thing that
// went wrong. An answer from a panel too old to report its traffic stops here: it would
// take users whose limit it can't share.
func (h *Handler) syncGatewayPeer(ctx context.Context, settings generated.GatewaySetting, peer generated.GatewayPeer) (reached bool, err error) {
	pingCtx, cancel := context.WithTimeout(ctx, gatewayPeerCallTimeout)
	ping, err := gatewayclient.Ping(pingCtx, peer.BaseUrl, peer.Secret)
	cancel()
	if err != nil {
		return false, err
	}
	if ping.Protocol < gatewayclient.ProtocolVersion {
		return true, errors.New("this panel runs an older version, update it first: until then nothing is sent to it and its servers are not offered")
	}

	statusCtx, cancel := context.WithTimeout(ctx, gatewayPeerCallTimeout)
	status, err := gatewayclient.Status(statusCtx, peer.BaseUrl, peer.Secret)
	cancel()
	if err != nil {
		return true, err
	}
	raw, err := json.Marshal(status)
	if err != nil {
		return true, err
	}
	if err := h.store.Cache.Set(ctx, cache.GatewayPeerStatusKey(peer.ID), string(raw), gatewayPeerStatusTTL); err != nil {
		return true, fmt.Errorf("could not cache its status: %w", err)
	}

	if err := h.readGatewayPeerUsage(ctx, settings, peer); err != nil {
		return true, fmt.Errorf("could not read its traffic: %w", err)
	}
	if err := h.pushGatewayPeerUsers(ctx, settings, peer); err != nil {
		return true, err
	}
	return true, nil
}

func (h *Handler) readGatewayPeerUsage(ctx context.Context, settings generated.GatewaySetting, peer generated.GatewayPeer) error {
	var after int64
	for {
		callCtx, cancel := context.WithTimeout(ctx, gatewayPeerCallTimeout)
		page, err := gatewayclient.Usage(callCtx, peer.BaseUrl, peer.Secret, settings.InstanceID, after, gatewayUsagePage)
		cancel()
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return nil
		}
		if err := h.addGatewayPeerUsage(ctx, peer.ID, page); err != nil {
			return err
		}
		if len(page) < gatewayUsagePage {
			return nil
		}
		after = page[len(page)-1].ID
	}
}

// addGatewayPeerUsage adds one page of a peer's counters to this panel's users, the way
// recordNodeTraffic adds a node's report: to the user, and to the admin who sold them,
// so their limit, on-hold start and reseller billing all see it. There's no node to
// credit it to, as the node is the peer's.
func (h *Handler) addGatewayPeerUsage(ctx context.Context, peerID int32, page []gatewayclient.PeerUsage) error {
	usernames := make([]string, 0, len(page))
	counters := make([]int64, 0, len(page))
	for _, u := range page {
		if u.UsedTraffic < 0 {
			return fmt.Errorf("invalid counter for %s", u.Username)
		}
		usernames = append(usernames, u.Username)
		counters = append(counters, u.UsedTraffic)
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
	claimed, err := q.ClaimGatewayPeerUsage(ctx, generated.ClaimGatewayPeerUsageParams{PeerID: peerID, Usernames: usernames, Counters: counters})
	if err != nil {
		return err
	}
	if len(claimed) == 0 {
		return tx.Commit(ctx)
	}
	byName := make(map[string]int64, len(claimed))
	moved := make([]string, 0, len(claimed))
	for _, c := range claimed {
		byName[c.Username] = c.Delta
		moved = append(moved, c.Username)
	}
	rows, err := q.GetUsersByUsernames(ctx, moved)
	if err != nil {
		return err
	}
	ids := make([]int32, 0, len(rows))
	deltas := make([]int64, 0, len(rows))
	admins := map[int32]int64{}
	for _, row := range rows {
		delta := byName[row.Username]
		if delta <= 0 {
			continue
		}
		ids = append(ids, row.ID)
		deltas = append(deltas, delta)
		if row.AdminID.Valid {
			if admins[row.AdminID.Int32] > math.MaxInt64-delta {
				return errors.New("admin traffic overflow")
			}
			admins[row.AdminID.Int32] += delta
		}
	}
	if len(ids) > 0 {
		// Keeps BulkIncrementUserUsage locking users in id order; see its comment.
		if _, err := tx.Exec(ctx, "SET LOCAL enable_hashjoin = off"); err != nil {
			return err
		}
		if _, err := q.BulkIncrementUserUsage(ctx, generated.BulkIncrementUserUsageParams{Ids: ids, Deltas: deltas}); err != nil {
			return err
		}
	}
	if len(admins) > 0 {
		adminIDs := make([]int32, 0, len(admins))
		for id := range admins {
			adminIDs = append(adminIDs, id)
		}
		slices.Sort(adminIDs)
		adminDeltas := make([]int64, len(adminIDs))
		for i, id := range adminIDs {
			adminDeltas[i] = admins[id]
		}
		if err := q.BulkIncrementAdminUsage(ctx, generated.BulkIncrementAdminUsageParams{Ids: adminIDs, Deltas: adminDeltas, QueueForResellerApi: queue}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// pushGatewayPeerUsers sends what the peer holds out of date, and takes back what it
// holds of users that are gone. A peer that says no to a user is recorded and shown to
// the admin, and that user isn't offered its servers. Anything else that goes wrong
// says nothing about the user, so it ends the pass for a later one to retry.
func (h *Handler) pushGatewayPeerUsers(ctx context.Context, settings generated.GatewaySetting, peer generated.GatewayPeer) error {
	q := h.store.Queries
	orphans, err := q.ListGatewayOrphanedUsers(ctx, generated.ListGatewayOrphanedUsersParams{PeerID: peer.ID, Limit: gatewayPushesPerPass})
	if err != nil {
		return err
	}
	origin := func(payload gatewayclient.UserSyncPayload) gatewayclient.UserSyncPayload {
		payload.OriginID = settings.InstanceID
		payload.OriginPanelName = settings.Name
		return payload
	}
	for _, orphan := range orphans {
		if orphan.State == gatewayStateSynced {
			callCtx, cancel := context.WithTimeout(ctx, gatewayPeerCallTimeout)
			_, err := gatewayclient.SyncUser(callCtx, peer.BaseUrl, peer.Secret, origin(gatewayclient.UserSyncPayload{Username: orphan.Username, Deleted: true}))
			cancel()
			var rejected *gatewayclient.RejectedError
			if err != nil && !errors.As(err, &rejected) {
				return err
			}
		}
		if err := q.DeleteGatewayPeerUser(ctx, generated.DeleteGatewayPeerUserParams{PeerID: peer.ID, Username: orphan.Username}); err != nil {
			return err
		}
	}

	pending, err := q.ListGatewayPendingUsers(ctx, generated.ListGatewayPendingUsersParams{
		PeerID: peer.ID, IncludeNew: peer.Enabled, RowLimit: gatewayPushesPerPass,
	})
	if err != nil {
		return err
	}

	pushCtx, stop := context.WithCancel(ctx)
	defer stop()
	work := make(chan generated.ListGatewayPendingUsersRow)
	var wg sync.WaitGroup
	var failure error
	var failed sync.Once
	fail := func(err error) {
		failed.Do(func() {
			failure = err
			stop()
		})
	}
	for range gatewayPushWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for user := range work {
				if err := h.pushGatewayPeerUser(pushCtx, peer, origin, user); err != nil {
					fail(err)
				}
			}
		}()
	}
feed:
	for _, user := range pending {
		select {
		case work <- user:
		case <-pushCtx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	return failure
}

func (h *Handler) pushGatewayPeerUser(ctx context.Context, peer generated.GatewayPeer, origin func(gatewayclient.UserSyncPayload) gatewayclient.UserSyncPayload, user generated.ListGatewayPendingUsersRow) error {
	var proxies map[string]json.RawMessage
	if err := json.Unmarshal(user.Proxies, &proxies); err != nil {
		return fmt.Errorf("could not read %s's credentials: %w", user.Username, err)
	}
	payload := origin(gatewayclient.UserSyncPayload{
		Username: user.Username, Status: user.Status, DataLimitResetStrategy: user.DataLimitResetStrategy, Proxies: proxies,
	})
	if user.DataLimit.Valid {
		payload.DataLimit = &user.DataLimit.Int64
	}
	if user.Expire.Valid {
		expire := int64(user.Expire.Int32)
		payload.Expire = &expire
	}

	callCtx, cancel := context.WithTimeout(ctx, gatewayPeerCallTimeout)
	result, err := gatewayclient.SyncUser(callCtx, peer.BaseUrl, peer.Secret, payload)
	cancel()
	row := generated.UpsertGatewayPeerUserParams{
		PeerID: peer.ID, Username: user.Username, State: gatewayStateSynced, StateHash: user.StateHash, PeerUsedTraffic: result.UsedTraffic,
	}
	if err != nil {
		var rejected *gatewayclient.RejectedError
		if !errors.As(err, &rejected) {
			return err
		}
		row.State, row.Detail = gatewayStateRejected, rejected.Detail
	}
	// Not the pass's context: the peer has this now, and stopping the pass mustn't lose
	// that it does.
	return h.store.Queries.UpsertGatewayPeerUser(context.WithoutCancel(ctx), row)
}

const (
	gatewayStateSynced   = "synced"
	gatewayStateRejected = "rejected"
)

// gatewayPeersHoldingUser is the set of peers that accepted this user.
func (h *Handler) gatewayPeersHoldingUser(ctx context.Context, username string) (map[int32]bool, error) {
	ids, err := h.store.Queries.ListGatewayPeersHoldingUser(ctx, username)
	if err != nil {
		return nil, err
	}
	out := make(map[int32]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out, nil
}

// pgTimePtr is a timestamp for a JSON answer, null when there is none.
func pgTimePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}
