package digestjob

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/report"
)

type fakeStore struct {
	admins []generated.Admin
	counts []generated.AdminOverviewCountsRow
	err    error
}

func (f *fakeStore) ListAdminsWithTelegram(context.Context) ([]generated.Admin, error) {
	return f.admins, f.err
}
func (f *fakeStore) AdminOverviewCounts(context.Context, generated.AdminOverviewCountsParams) ([]generated.AdminOverviewCountsRow, error) {
	return f.counts, nil
}

type fakeSender struct {
	got []string
	ret bool
}

func (f *fakeSender) AdminDigest(_ context.Context, a report.AdminRef, e, l int64) bool {
	f.got = append(f.got, a.Username)
	return f.ret
}

type fakeClaimer struct{ m map[string]bool }

func (f *fakeClaimer) SetNX(_ context.Context, key, _ string, _ time.Duration) (bool, error) {
	if f.m[key] {
		return false, nil
	}
	f.m[key] = true
	return true, nil
}

func admin(id int32, name string) generated.Admin {
	return generated.Admin{ID: id, Username: name, TelegramID: pgtype.Int8{Int64: int64(1000 + id), Valid: true}}
}

func row(id int32, expiring, low int64) generated.AdminOverviewCountsRow {
	return generated.AdminOverviewCountsRow{AdminID: pgtype.Int4{Int32: id, Valid: true}, Expiring3d: expiring, LowData: low}
}

func deps(st *fakeStore, s *fakeSender, c *fakeClaimer) Deps {
	return Deps{Store: st, Sender: s, Claimer: c, HourUTC: 6, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestDigestIsSentOncePerDayToAdminsWithSomethingToReport(t *testing.T) {
	st := &fakeStore{
		admins: []generated.Admin{admin(1, "amir"), admin(2, "quiet"), admin(3, "low")},
		counts: []generated.AdminOverviewCountsRow{row(1, 4, 2), row(2, 0, 0), row(3, 0, 5)},
	}
	s, c := &fakeSender{ret: true}, &fakeClaimer{m: map[string]bool{}}
	d := deps(st, s, c)
	day := time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)

	if n := Tick(context.Background(), d, day.Add(-3*time.Hour)); n != 0 || len(s.got) != 0 {
		t.Fatalf("before the hour nothing is sent: %d", n)
	}
	if n := Tick(context.Background(), d, day); n != 2 {
		t.Fatalf("sent = %d, want amir and low (quiet has nothing to report)", n)
	}
	if n := Tick(context.Background(), d, day.Add(2*time.Hour)); n != 0 {
		t.Fatalf("a second tick the same day sent %d", n)
	}
	if len(s.got) != 2 || s.got[0] != "amir" || s.got[1] != "low" {
		t.Errorf("recipients = %v", s.got)
	}
	if n := Tick(context.Background(), d, day.Add(24*time.Hour)); n != 2 {
		t.Errorf("the next day sends again: %d", n)
	}
}

func TestDigestSurvivesAFailingStoreAndSender(t *testing.T) {
	st := &fakeStore{err: errors.New("db down")}
	s, c := &fakeSender{ret: false}, &fakeClaimer{m: map[string]bool{}}
	if n := Tick(context.Background(), deps(st, s, c), time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)); n != 0 {
		t.Errorf("sent %d", n)
	}
	st.err = nil
	st.admins = []generated.Admin{admin(1, "amir")}
	st.counts = []generated.AdminOverviewCountsRow{row(1, 1, 0)}
	if n := Tick(context.Background(), deps(st, s, c), time.Date(2026, 10, 19, 8, 0, 0, 0, time.UTC)); n != 0 || len(s.got) != 1 {
		t.Errorf("a sender that sends nothing is not counted: %d %v", n, s.got)
	}
}

func TestDue(t *testing.T) {
	if Due(time.Date(2026, 1, 1, 5, 59, 0, 0, time.UTC), 6) || !Due(time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC), 6) || !Due(time.Date(2026, 1, 1, 23, 0, 0, 0, time.UTC), 6) {
		t.Error("Due")
	}
}
