package reviewjob

import (
	"context"
	"encoding/json"
	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/report"
	"github.com/legendary1205/rapido-go/internal/txscope"
	"log/slog"
	"strings"
	"time"
)

// Every batch is bounded. Writes and notification records commit together.
func PeriodicPolicies(ctx context.Context, q *generated.Queries, d *report.Dispatcher, logger *slog.Logger) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pool := txscope.Pool(ctx)
		if pool == nil {
			return
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			continue
		}
		batchCtx := txscope.WithTransaction(ctx, tx)
		queries := q.WithTx(tx)
		if _, err = tx.Exec(batchCtx, "SELECT pg_advisory_xact_lock_shared(814720033)"); err == nil {
			var users []generated.User
			users, err = queries.ResetDuePeriodicUsers(batchCtx)
			if err == nil {
				for _, u := range users {
					d.UserDataResetByNext(batchCtx, toUserSummary(u), lookupAdminRef(batchCtx, queries, u.AdminID, logger))
				}
			}
			if err == nil {
				users, err = queries.RemoveAutoExpiredUsers(batchCtx)
				if err == nil {
					for _, u := range users {
						d.UserDeleted(batchCtx, u.Username, "automatic policy", lookupAdminRef(batchCtx, queries, u.AdminID, logger))
						if !u.SyncedFromPanelName.Valid {
							peers, peerErr := queries.ListGatewayPeers(batchCtx)
							if peerErr != nil {
								err = peerErr
								break
							}
							raw, _ := json.Marshal(map[string]any{"username": u.Username, "deleted": true})
							for _, peer := range peers {
								if !peer.Enabled {
									continue
								}
								payload, _ := json.Marshal(map[string]any{"body": json.RawMessage(raw), "ordering_key": u.Username, "headers": map[string]string{"Authorization": "Bearer " + peer.Secret}})
								if e := queries.EnqueueDelivery(batchCtx, generated.EnqueueDeliveryParams{Kind: "gateway", Destination: strings.TrimRight(peer.BaseUrl, "/") + "/api/internal/gateway/users/sync", Payload: payload}); e != nil {
									err = e
									break
								}
							}
						}
					}
				}
			}
		}
		if err == nil {
			err = tx.Commit(batchCtx)
			if err == nil {
				txscope.RunAfterCommit(batchCtx)
			}
		} else {
			logger.Error("periodic user policy rolled back", "error", err)
		}
		_ = tx.Rollback(context.Background())
	}
}
