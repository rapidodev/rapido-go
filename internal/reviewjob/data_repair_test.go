package reviewjob

import (
	"context"
	"testing"
	"time"
)

// backdateUpdate makes the update that ran 00032 a month and a half old for this
// test, so that resets can be placed around it.
func (h *testHarness) backdateUpdate(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	var original time.Time
	if err := h.pool.QueryRow(ctx, "SELECT max(tstamp) FROM goose_db_version WHERE version_id=32 AND is_applied").Scan(&original); err != nil {
		t.Skipf("no goose history in this database: %v", err)
	}
	t.Cleanup(func() {
		h.pool.Exec(ctx, "UPDATE goose_db_version SET tstamp=$1 WHERE version_id=32", original)
	})
	h.execRaw(t, "UPDATE goose_db_version SET tstamp=now()-interval '45 days' WHERE version_id=32")
}

func (h *testHarness) usageLogs(t *testing.T, id int32) int {
	t.Helper()
	var n int
	if err := h.pool.QueryRow(context.Background(), "SELECT count(*) FROM user_usage_logs WHERE user_id=$1", id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The sweep here is the job's own query, so this also holds the migration to what
// that query really writes, such as the log row and the anchor sharing a timestamp.
func TestUsageTheFirstSweepWipedComesBack(t *testing.T) {
	h := newTestHarness(t)
	h.backdateUpdate(t)
	ctx := context.Background()

	old := func(name, status, strategy string, limit, used int64) int32 {
		u := h.createTestUser(t, name, status, limit, used, 0)
		h.execRaw(t, "UPDATE users SET data_limit_reset_strategy=$2, created_at=now()-interval '2 years', last_usage_reset_at=NULL WHERE id=$1", u.ID, strategy)
		return u.ID
	}
	midPlan := old("swept_midplan", "active", "month", 100_000, 30_000)
	limited := old("swept_limited", "limited", "month", 40_000, 40_000)
	resetByAdmin := old("swept_then_reset", "active", "month", 100_000, 20_000)
	idleAtSweep := old("idle_at_sweep", "active", "month", 100_000, 0)
	young := h.createTestUser(t, "young", "active", 100_000, 7_000, 0)
	h.execRaw(t, "UPDATE users SET data_limit_reset_strategy='month', created_at=now()-interval '35 days', last_usage_reset_at=NULL WHERE id=$1", young.ID)

	if due, err := h.q.ResetDuePeriodicUsers(ctx); err != nil {
		t.Fatal(err)
	} else if len(due) != 5 {
		t.Fatalf("the sweep took %d users, want 5", len(due))
	}
	// All of that was three days ago, and the users have been online since.
	h.execRaw(t, "UPDATE user_usage_logs SET reset_at=reset_at-interval '3 days'")
	h.execRaw(t, "UPDATE users SET last_usage_reset_at=last_usage_reset_at-interval '3 days'")
	h.execRaw(t, "UPDATE users SET used_traffic=1000")
	// Younger than a period at the sweep after all: that reset was a real one.
	h.execRaw(t, "UPDATE users SET created_at=now()-interval '20 days' WHERE id=$1", young.ID)

	// A day ago an admin reset one swept user, and one the sweep passed over with no
	// usage. Neither should come back: the admin wanted these at zero.
	h.execRaw(t, "INSERT INTO user_usage_logs(user_id,used_traffic_at_reset,reset_at) VALUES ($1,1000,now()-interval '1 day')", resetByAdmin)
	h.execRaw(t, "UPDATE users SET used_traffic=0, last_usage_reset_at=now()-interval '1 day' WHERE id=$1", resetByAdmin)
	h.execRaw(t, "INSERT INTO user_usage_logs(user_id,used_traffic_at_reset,reset_at) SELECT id,1000,last_usage_reset_at+interval '2 days' FROM users WHERE id=$1", idleAtSweep)
	h.execRaw(t, "UPDATE users SET used_traffic=0, last_usage_reset_at=last_usage_reset_at+interval '2 days' WHERE id=$1", idleAtSweep)

	// loggedAt gives a user one log row, with the anchor on the same instant as the
	// job would leave it, at the sweep or at the time given.
	loggedAt := func(id int32, at string) {
		h.execRaw(t, "INSERT INTO user_usage_logs(user_id,used_traffic_at_reset,reset_at) VALUES ($1,9000,"+at+")", id)
		h.execRaw(t, "UPDATE users SET last_usage_reset_at=(SELECT reset_at FROM user_usage_logs WHERE user_id=$1) WHERE id=$1", id)
	}
	const atTheSweep = "(SELECT reset_at FROM user_usage_logs l JOIN users u ON u.id=l.user_id WHERE u.username='swept_midplan')"

	// A later pass that logged nothing but moved the anchor.
	movedOn := old("anchor_moved_on", "active", "month", 100_000, 500)
	loggedAt(movedOn, atTheSweep)
	h.execRaw(t, "UPDATE users SET last_usage_reset_at=now()-interval '1 hour' WHERE id=$1", movedOn)
	// A period over since then, one reset from before the update, and no strategy.
	dayOver := old("day_period_over", "active", "day", 100_000, 500)
	loggedAt(dayOver, atTheSweep)
	before := old("reset_before_update", "active", "year", 100_000, 500)
	loggedAt(before, "now()-interval '60 days'")
	noStrategy := h.createTestUser(t, "no_strategy", "active", 100_000, 500, 0)
	h.execRaw(t, "UPDATE users SET created_at=now()-interval '2 years' WHERE id=$1", noStrategy.ID)
	loggedAt(noStrategy.ID, atTheSweep)

	anchorBefore := h.reload(t, midPlan).LastUsageResetAt.Time
	h.execRaw(t, migrationUp(t, "00037_restore_wiped_usage.sql"))

	got := h.reload(t, midPlan)
	if got.UsedTraffic != 31_000 {
		t.Errorf("a user mid plan: used=%d, want what the sweep took plus what came since", got.UsedTraffic)
	}
	if !got.LastUsageResetAt.Time.Equal(anchorBefore) {
		t.Errorf("the anchor moved from %v to %v", anchorBefore, got.LastUsageResetAt.Time)
	}
	if n := h.usageLogs(t, midPlan); n != 0 {
		t.Errorf("%d log rows left, so lifetime usage counts the traffic twice", n)
	}
	if got := h.reload(t, limited); got.UsedTraffic != 41_000 {
		t.Errorf("a limited user: used=%d", got.UsedTraffic)
	}

	for name, want := range map[string]struct {
		id   int32
		used int64
		logs int
	}{
		"reset by an admin since":     {resetByAdmin, 0, 2},
		"reset by an admin, no sweep": {idleAtSweep, 0, 1},
		"young at the sweep":          {young.ID, 1000, 1},
		"anchor moved on":             {movedOn, 500, 1},
		"period over":                 {dayOver, 500, 1},
		"reset before the update":     {before, 500, 1},
		"no strategy":                 {noStrategy.ID, 500, 1},
	} {
		got := h.reload(t, want.id)
		if got.UsedTraffic != want.used {
			t.Errorf("%s: used=%d, want %d", name, got.UsedTraffic, want.used)
		}
		if n := h.usageLogs(t, want.id); n != want.logs {
			t.Errorf("%s: %d log rows, want %d", name, n, want.logs)
		}
	}

	// Back over the limit means back to where the update found them.
	if err := review(ctx, h.q, testDispatcher(h.q), testLogger()); err != nil {
		t.Fatal(err)
	}
	if got := h.reload(t, limited); got.Status != "limited" {
		t.Errorf("a user back over their limit: status=%s", got.Status)
	}
	if got := h.reload(t, midPlan); got.Status != "active" {
		t.Errorf("a user under their limit: status=%s", got.Status)
	}
}

func TestPlansThatLostTheirExpiryRunAgain(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	const month = 2592000
	expires := time.Now().Add(time.Hour).Unix()

	hold := func(name, status string, expire int64) int32 {
		u := h.createTestUser(t, name, status, 1000, 0, expire)
		h.execRaw(t, "UPDATE users SET on_hold_expire_duration=$2, edit_at=now() WHERE id=$1", u.ID, int64(month))
		return u.ID
	}
	neverOnline := hold("damaged_idle", "active", 0)
	wasOnline := hold("damaged_online", "active", 0)
	h.execRaw(t, "UPDATE users SET online_at=now()+interval '1 minute' WHERE id=$1", wasOnline)
	zeroExpire := hold("damaged_zero_expire", "active", 0)
	h.execRaw(t, "UPDATE users SET expire=0 WHERE id=$1", zeroExpire)
	limited := hold("damaged_limited", "limited", 0)
	h.execRaw(t, "UPDATE users SET used_traffic=1000 WHERE id=$1", limited)
	waiting := hold("really_waiting", "on_hold", 0)
	withExpiry := hold("has_expiry", "active", expires)
	disabled := hold("disabled_waiting", "disabled", 0)
	plain := h.createTestUser(t, "plain_unlimited", "active", 1000, 0, 0)

	h.execRaw(t, migrationUp(t, "00038_restore_on_hold_users.sql"))

	for name, id := range map[string]int32{"idle": neverOnline, "online": wasOnline, "zero expire": zeroExpire} {
		if got := h.reload(t, id); got.Status != "on_hold" || !got.OnHoldExpireDuration.Valid {
			t.Errorf("%s: status=%s duration=%v, want back on hold", name, got.Status, got.OnHoldExpireDuration)
		}
	}
	if got := h.reload(t, limited); got.Status != "limited" || got.OnHoldExpireDuration.Valid || got.Expire.Int32 < int32(time.Now().Unix()) {
		t.Errorf("limited: status=%s expire=%v duration=%v, want still limited with the plan running", got.Status, got.Expire, got.OnHoldExpireDuration)
	}
	if got := h.reload(t, waiting); got.Status != "on_hold" {
		t.Errorf("a user really waiting: status=%s", got.Status)
	}
	if got := h.reload(t, withExpiry); got.Status != "active" || int64(got.Expire.Int32) != expires {
		t.Errorf("a user with an expiry: status=%s expire=%v", got.Status, got.Expire)
	}
	if got := h.reload(t, disabled); got.Status != "disabled" {
		t.Errorf("a disabled user: status=%s", got.Status)
	}
	if got := h.reload(t, plain.ID); got.Status != "active" || got.Expire.Valid {
		t.Errorf("a user meant to have no expiry: status=%s expire=%v", got.Status, got.Expire)
	}

	// Whoever connected starts their plan on the next review, and nobody else does.
	if err := review(ctx, h.q, testDispatcher(h.q), testLogger()); err != nil {
		t.Fatal(err)
	}
	got := h.reload(t, wasOnline)
	if got.Status != "active" || !got.Expire.Valid {
		t.Fatalf("a user who has connected: status=%s expire=%v", got.Status, got.Expire)
	}
	if left := int64(got.Expire.Int32) - time.Now().Unix(); left < month-60 || left > month+60 {
		t.Errorf("a user who has connected has %d seconds left, want a whole plan", left)
	}
	if got := h.reload(t, neverOnline); got.Status != "on_hold" {
		t.Errorf("a user who hasn't connected: status=%s", got.Status)
	}
}
