package tunnelwatch

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

// DBStore is the Postgres-backed Store.
type DBStore struct {
	Q *generated.Queries
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func f8(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

func f4(v *float64) pgtype.Float4 {
	if v == nil {
		return pgtype.Float4{}
	}
	return pgtype.Float4{Float32: float32(*v), Valid: true}
}

// InsertSample writes one round.
func (d DBStore) InsertSample(ctx context.Context, s Sample) error {
	p := generated.InsertTunnelSampleParams{
		TunnelID: s.TunnelID, At: ts(s.At),
		RelayRxBps: f8(s.RelayRxBps), RelayTxBps: f8(s.RelayTxBps), NodeRxBps: f8(s.NodeRxBps), NodeTxBps: f8(s.NodeTxBps),
		Cpu: f4(s.CPU),
	}
	if s.Connections != nil {
		p.Connections = pgtype.Int4{Int32: *s.Connections, Valid: true}
	}
	if s.V6 != nil {
		loss := s.V6.Loss
		p.V6Loss, p.V6RttMs, p.V6JitterMs = f4(&loss), f4(s.V6.RTT), f4(s.V6.Jitter)
	}
	if s.V4 != nil {
		loss := s.V4.Loss
		p.V4Loss, p.V4RttMs, p.V4JitterMs = f4(&loss), f4(s.V4.RTT), f4(s.V4.Jitter)
	}
	if s.HAActive != nil {
		p.HaActive = pgtype.Text{String: *s.HAActive, Valid: true}
	}
	if s.HAPrimaryUp != nil {
		p.HaPrimaryUp = pgtype.Bool{Bool: *s.HAPrimaryUp, Valid: true}
	}
	if s.HABackupUp != nil {
		p.HaBackupUp = pgtype.Bool{Bool: *s.HABackupUp, Valid: true}
	}
	return d.Q.InsertTunnelSample(ctx, p)
}

// InsertEvent writes one event.
func (d DBStore) InsertEvent(ctx context.Context, e Event) error {
	p := generated.InsertTunnelEventParams{
		TunnelID: e.TunnelID, At: ts(e.At), Kind: e.Kind, Severity: e.Severity, Detail: e.Detail,
	}
	if e.DurationSeconds != nil {
		p.DurationSeconds = pgtype.Int4{Int32: *e.DurationSeconds, Valid: true}
	}
	_, err := d.Q.InsertTunnelEvent(ctx, p)
	return err
}

// LastEventTimes returns the latest event time per kind.
func (d DBStore) LastEventTimes(ctx context.Context, tunnelID int32, since time.Time) (map[string]time.Time, error) {
	rows, err := d.Q.LastTunnelEventTimes(ctx, generated.LastTunnelEventTimesParams{TunnelID: tunnelID, At: ts(since)})
	if err != nil {
		return nil, err
	}
	out := make(map[string]time.Time, len(rows))
	for _, r := range rows {
		if r.LastAt.Valid {
			out[r.Kind] = r.LastAt.Time
		}
	}
	return out, nil
}

// Prune deletes history past its retention.
func (d DBStore) Prune(ctx context.Context, samplesBefore, eventsBefore time.Time) (int64, int64, error) {
	s, err := d.Q.PruneTunnelSamples(ctx, ts(samplesBefore))
	if err != nil {
		return 0, 0, err
	}
	e, err := d.Q.PruneTunnelEvents(ctx, ts(eventsBefore))
	return s, e, err
}
