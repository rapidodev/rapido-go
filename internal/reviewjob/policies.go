package reviewjob

import (
	"context"
	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/report"
	"github.com/legendary1205/rapido-go/internal/txscope"
	"log/slog"
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
