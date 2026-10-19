// Package digestjob sends each admin that has a Telegram id one message a day
// with the number of their configs that expire within three days and that have
// 10% or less of their data left - the two things a reseller can still act on.
package digestjob

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/report"
)

// Store is the database part.
type Store interface {
	ListAdminsWithTelegram(ctx context.Context) ([]generated.Admin, error)
	AdminOverviewCounts(ctx context.Context, arg generated.AdminOverviewCountsParams) ([]generated.AdminOverviewCountsRow, error)
}

// Sender delivers a digest; it returns false when nothing was sent.
type Sender interface {
	AdminDigest(ctx context.Context, admin report.AdminRef, expiring3d, lowData int64) bool
}

// Claimer records that a digest was handled today, so it goes out once a day
// even across restarts and several panel processes.
type Claimer interface {
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
}

// Deps are the job's collaborators.
type Deps struct {
	Store   Store
	Sender  Sender
	Claimer Claimer
	// HourUTC is the UTC hour from which the day's digest may go out.
	HourUTC int
	Logger  *slog.Logger
}

// Due reports whether the day's digest may be sent at now.
func Due(now time.Time, hourUTC int) bool { return now.UTC().Hour() >= hourUTC }

func claimKey(now time.Time, adminID int32) string {
	return "digest:" + now.UTC().Format("2006-01-02") + ":" + strconv.Itoa(int(adminID))
}

// Tick sends the digests that are due and not yet sent today, and returns how
// many went out. An admin with nothing to report is not messaged (and is not
// retried until tomorrow either).
func Tick(ctx context.Context, d Deps, now time.Time) int {
	if !Due(now, d.HourUTC) {
		return 0
	}
	admins, err := d.Store.ListAdminsWithTelegram(ctx)
	if err != nil {
		d.Logger.Warn("digest: could not list admins", "error", err)
		return 0
	}
	if len(admins) == 0 {
		return 0
	}
	nowTS := int32(now.Unix())
	counts, err := d.Store.AdminOverviewCounts(ctx, generated.AdminOverviewCountsParams{
		Now: nowTS, In3: int32(now.Add(72 * time.Hour).Unix()), In7: int32(now.Add(7 * 24 * time.Hour).Unix()), AdminID: pgtype.Int4{},
	})
	if err != nil {
		d.Logger.Warn("digest: could not count configs", "error", err)
		return 0
	}
	byAdmin := map[int32]generated.AdminOverviewCountsRow{}
	for _, c := range counts {
		if c.AdminID.Valid {
			byAdmin[c.AdminID.Int32] = c
		}
	}
	sent := 0
	for _, a := range admins {
		ok, err := d.Claimer.SetNX(ctx, claimKey(now, a.ID), "1", 48*time.Hour)
		if err != nil {
			d.Logger.Warn("digest: could not claim", "admin", a.Username, "error", err)
			continue
		}
		if !ok {
			continue // already handled today
		}
		c := byAdmin[a.ID]
		if c.Expiring3d == 0 && c.LowData == 0 {
			continue
		}
		ref := report.AdminRef{Username: a.Username, TelegramID: &a.TelegramID.Int64}
		if !a.TelegramID.Valid {
			continue
		}
		if d.Sender.AdminDigest(ctx, ref, c.Expiring3d, c.LowData) {
			sent++
		}
	}
	return sent
}

// Run ticks every interval until ctx is done.
func Run(ctx context.Context, d Deps, interval time.Duration) {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if n := Tick(ctx, d, time.Now()); n > 0 {
			d.Logger.Info("digest: sent", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
