package reviewjob

import (
	"context"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/legendary1205/rapido-go/internal/db/generated"
)

const fifteenYears = 15 * 365 * 24 * 60 * 60

// migrationUp is the Up half of a migration file, to run against rows a test set
// up the way an older panel left them.
func migrationUp(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("../db/migrations/" + name)
	if err != nil {
		t.Fatal(err)
	}
	up, _, found := strings.Cut(string(raw), "-- +goose Down")
	if !found {
		t.Fatalf("%s has no Down section", name)
	}
	return strings.TrimPrefix(up, "-- +goose Up")
}

func (h *testHarness) reload(t *testing.T, id int32) generated.User {
	t.Helper()
	u, err := h.q.GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// Updating past the commit that added the periodic job zeroed the usage of every
// user on a reset strategy and reactivated the limited ones, as an empty anchor
// fell back to created_at and read as a period long over.
func TestAnUpdateDoesNotResetUsersWhosePeriodWasNeverStarted(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	old := h.createTestUser(t, "month_limited", "limited", 40_000, 40_000, 0)
	h.execRaw(t, "UPDATE users SET data_limit_reset_strategy='month', created_at=now()-interval '3 months', last_usage_reset_at=NULL WHERE id=$1", old.ID)
	midPlan := h.createTestUser(t, "month_midplan", "active", 100_000, 30_000, 0)
	h.execRaw(t, "UPDATE users SET data_limit_reset_strategy='week', created_at=now()-interval '3 months', last_usage_reset_at=NULL WHERE id=$1", midPlan.ID)
	young := h.createTestUser(t, "month_young", "active", 100_000, 10, 0)
	h.execRaw(t, "UPDATE users SET data_limit_reset_strategy='month', created_at=now()-interval '3 days', last_usage_reset_at=NULL WHERE id=$1", young.ID)
	noReset := h.createTestUser(t, "no_reset_old", "active", 100_000, 30_000, 0)
	h.execRaw(t, "UPDATE users SET created_at=now()-interval '3 months', last_usage_reset_at=NULL WHERE id=$1", noReset.ID)
	anchor := time.Now().Add(-24 * time.Hour).UTC().Truncate(time.Second)
	reset := h.createTestUser(t, "already_reset", "active", 100_000, 500, 0)
	h.execRaw(t, "UPDATE users SET data_limit_reset_strategy='month', created_at=now()-interval '3 months', last_usage_reset_at=$2 WHERE id=$1", reset.ID, anchor)

	h.execRaw(t, migrationUp(t, "00036_usage_reset_anchor.sql"))

	due, err := h.q.ResetDuePeriodicUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Errorf("the first pass after the update reset %d users", len(due))
	}

	if got := h.reload(t, old.ID); got.UsedTraffic != 40_000 || got.Status != "limited" {
		t.Errorf("a limited user: used=%d status=%s, want both untouched", got.UsedTraffic, got.Status)
	} else if !got.LastUsageResetAt.Valid {
		t.Error("a due user got no anchor")
	}
	if got := h.reload(t, midPlan.ID); got.UsedTraffic != 30_000 {
		t.Errorf("a user mid plan: used=%d", got.UsedTraffic)
	}
	if got := h.reload(t, young.ID); got.LastUsageResetAt.Valid {
		t.Error("a user younger than a period lost created_at as their anchor")
	}
	if got := h.reload(t, noReset.ID); got.LastUsageResetAt.Valid {
		t.Error("a user with no strategy got an anchor they don't need")
	}
	if got := h.reload(t, reset.ID); !got.LastUsageResetAt.Time.UTC().Equal(anchor) {
		t.Errorf("an anchor already set moved to %v", got.LastUsageResetAt.Time)
	}

	// The next period does still end.
	h.execRaw(t, "UPDATE users SET last_usage_reset_at=now()-interval '2 months' WHERE id=$1", old.ID)
	due, err = h.q.ResetDuePeriodicUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != old.ID || due[0].UsedTraffic != 0 || due[0].Status != "active" {
		t.Errorf("a period that has really ended: %+v", due)
	}
}

func TestChangingTheStrategyStartsAPeriodInsteadOfEndingOne(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	u := h.createTestUser(t, "switches", "active", 100_000, 40_000, 0)
	h.execRaw(t, "UPDATE users SET created_at=now()-interval '6 months', last_usage_reset_at=NULL WHERE id=$1", u.ID)

	updated, err := h.q.UpdateUserCore(ctx, generated.UpdateUserCoreParams{
		ID: u.ID, Status: "active", DataLimit: u.DataLimit, DataLimitResetStrategy: "month", Expire: u.Expire,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !updated.LastUsageResetAt.Valid {
		t.Error("no anchor after a strategy change")
	}
	due, err := h.q.ResetDuePeriodicUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Errorf("switching to monthly wiped the usage on the next pass: %+v", due)
	}

	// An edit that leaves the strategy alone leaves the period alone.
	h.execRaw(t, "UPDATE users SET last_usage_reset_at=now()-interval '10 days' WHERE id=$1", u.ID)
	before := h.reload(t, u.ID).LastUsageResetAt.Time
	if _, err := h.q.UpdateUserCore(ctx, generated.UpdateUserCoreParams{
		ID: u.ID, Status: "active", DataLimit: u.DataLimit, DataLimitResetStrategy: "month", Expire: u.Expire,
	}); err != nil {
		t.Fatal(err)
	}
	if after := h.reload(t, u.ID).LastUsageResetAt.Time; !after.Equal(before) {
		t.Errorf("an unrelated edit moved the anchor from %v to %v", before, after)
	}
}

func TestEveryWayOfResettingUsageRestartsThePeriod(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	bulk := h.createTestUser(t, "bulk_reset", "active", 100_000, 40_000, 0)
	h.execRaw(t, "UPDATE users SET data_limit_reset_strategy='month', last_usage_reset_at=now()-interval '29 days' WHERE id=$1", bulk.ID)
	if err := h.q.ResetUsersUsageByIDs(ctx, []int32{bulk.ID}); err != nil {
		t.Fatal(err)
	}

	plan := h.createTestUser(t, "plan_reset", "active", 1000, 1000, 0)
	h.execRaw(t, "UPDATE users SET data_limit_reset_strategy='month', last_usage_reset_at=now()-interval '29 days' WHERE id=$1", plan.ID)
	if _, err := h.q.UpsertNextPlan(ctx, generated.UpsertNextPlanParams{UserID: plan.ID, DataLimit: 5000, FireOnEither: true}); err != nil {
		t.Fatal(err)
	}
	if err := review(ctx, h.q, testDispatcher(h.q), testLogger()); err != nil {
		t.Fatal(err)
	}

	for name, id := range map[string]int32{"bulk": bulk.ID, "next plan": plan.ID} {
		got := h.reload(t, id)
		if got.UsedTraffic != 0 {
			t.Errorf("%s: used=%d", name, got.UsedTraffic)
		}
		if time.Since(got.LastUsageResetAt.Time) > time.Minute {
			t.Errorf("%s: anchor left at %v, so a period ending tomorrow wipes the new usage", name, got.LastUsageResetAt.Time)
		}
	}
}

// A plan the API allowed used to overflow the int4 column on every tick, and the
// job's `continue` then left the user never limited and never expired.
func TestAVeryLongPlanFiresInsteadOfLeavingTheUserUnlimited(t *testing.T) {
	h := newTestHarness(t)
	u := h.createTestUser(t, "long_plan", "active", 1000, 1000, 0)
	if _, err := h.q.UpsertNextPlan(context.Background(), generated.UpsertNextPlanParams{
		UserID: u.ID, DataLimit: 5000, Expire: pgInt4(fifteenYears), FireOnEither: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := review(context.Background(), h.q, testDispatcher(h.q), testLogger()); err != nil {
		t.Fatal(err)
	}
	got := h.reload(t, u.ID)
	if got.Status != "active" || got.UsedTraffic != 0 || got.DataLimit.Int64 != 5000 {
		t.Errorf("the plan didn't fire: %+v", got)
	}
	if got.Expire.Int32 != math.MaxInt32 {
		t.Errorf("expire = %d, want it held to the column's limit", got.Expire.Int32)
	}
}

func TestAVeryLongHoldStillActivates(t *testing.T) {
	h := newTestHarness(t)
	u := h.createTestUser(t, "long_hold", "on_hold", 0, 0, 0)
	h.execRaw(t, "UPDATE users SET on_hold_expire_duration=$2, online_at=now()+interval '1 minute' WHERE id=$1", u.ID, int64(fifteenYears))
	if err := review(context.Background(), h.q, testDispatcher(h.q), testLogger()); err != nil {
		t.Fatal(err)
	}
	got := h.reload(t, u.ID)
	if got.Status != "active" || got.Expire.Int32 != math.MaxInt32 {
		t.Errorf("status=%s expire=%d: stuck on hold means served forever", got.Status, got.Expire.Int32)
	}
}

// Whatever makes a plan fail, the user still gets the status they earned.
func TestAPlanThatCannotFireStillCutsTheUserOff(t *testing.T) {
	h := newTestHarness(t)
	u := h.createTestUser(t, "plan_breaks", "active", 1000, 1000, 0)
	if _, err := h.q.UpsertNextPlan(context.Background(), generated.UpsertNextPlanParams{
		UserID: u.ID, DataLimit: 5000, FireOnEither: true,
	}); err != nil {
		t.Fatal(err)
	}
	h.execRaw(t, `CREATE OR REPLACE FUNCTION test_fail_plan_reset() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'plan cannot fire'; END $$ LANGUAGE plpgsql`)
	h.execRaw(t, `CREATE TRIGGER test_fail_plan_reset BEFORE UPDATE ON users FOR EACH ROW
		WHEN (NEW.used_traffic = 0 AND OLD.used_traffic > 0) EXECUTE FUNCTION test_fail_plan_reset()`)
	t.Cleanup(func() {
		h.pool.Exec(context.Background(), "DROP TRIGGER IF EXISTS test_fail_plan_reset ON users")
		h.pool.Exec(context.Background(), "DROP FUNCTION IF EXISTS test_fail_plan_reset()")
	})

	if err := review(context.Background(), h.q, testDispatcher(h.q), testLogger()); err != nil {
		t.Fatal(err)
	}
	if got := h.reload(t, u.ID); got.Status != "limited" {
		t.Errorf("status = %s, want limited: a plan that can't fire must not mean no limit", got.Status)
	}
}

func TestEnablingAnAdminsUsersPutsWaitingPlansBackOnHold(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	h.execRaw(t, "INSERT INTO admins (username, hashed_password) VALUES ('plan_owner', 'x') ON CONFLICT DO NOTHING")
	var adminID int32
	if err := h.pool.QueryRow(ctx, "SELECT id FROM admins WHERE username='plan_owner'").Scan(&adminID); err != nil {
		t.Fatal(err)
	}
	waiting := h.createTestUser(t, "was_waiting", "on_hold", 0, 0, 0)
	h.execRaw(t, "UPDATE users SET admin_id=$2, on_hold_expire_duration=2592000 WHERE id=$1", waiting.ID, adminID)
	started := h.createTestUser(t, "was_started", "active", 0, 0, time.Now().Add(time.Hour).Unix())
	h.execRaw(t, "UPDATE users SET admin_id=$2 WHERE id=$1", started.ID, adminID)

	if _, err := h.q.DisableActiveUsersByAdminID(ctx, pgInt4(int64(adminID))); err != nil {
		t.Fatal(err)
	}
	if got := h.reload(t, waiting.ID); got.Status != "disabled" {
		t.Fatalf("status = %s", got.Status)
	}
	if _, err := h.q.ActivateDisabledUsersByAdminID(ctx, pgInt4(int64(adminID))); err != nil {
		t.Fatal(err)
	}
	if got := h.reload(t, waiting.ID); got.Status != "on_hold" || got.Expire.Valid {
		t.Errorf("waiting: status=%s expire=%v, want back on hold, not active with no expiry", got.Status, got.Expire)
	}
	if got := h.reload(t, started.ID); got.Status != "active" {
		t.Errorf("started: status=%s", got.Status)
	}
}

func TestAPlanWithNoExpiryLeavesAWaitingUserWaiting(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	u := h.createTestUser(t, "waiting_plan", "on_hold", 0, 0, 0)
	h.execRaw(t, "UPDATE users SET on_hold_expire_duration=2592000 WHERE id=$1", u.ID)
	if _, err := h.q.UpsertNextPlan(ctx, generated.UpsertNextPlanParams{UserID: u.ID, DataLimit: 5000, FireOnEither: true}); err != nil {
		t.Fatal(err)
	}
	got, err := h.q.ResetUserByNextPlan(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "on_hold" || !got.OnHoldExpireDuration.Valid || got.DataLimit.Int64 != 5000 {
		t.Errorf("status=%s duration=%v limit=%v", got.Status, got.OnHoldExpireDuration, got.DataLimit)
	}
}
