package reviewjob

import (
	"context"
	"testing"
)

// A copy's traffic is one panel's share of the limit, for its origin to add up. Limiting
// it on that would cut off a user the origin still has room for, and a renewal there
// wouldn't help, as nothing resets the count here.
func TestGatewayCopyIsNotLimitedOrResetByItsOwnShare(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	h.execRaw(t, `INSERT INTO users (username, status, data_limit, used_traffic, data_limit_reset_strategy, synced_from_panel_name, synced_origin_id, created_at, online_at)
		VALUES ('copy_over', 'active', 1000, 5000, 'day', 'origin', 'origin-id', now() - interval '3 days', NULL),
		       ('local_over', 'active', 1000, 5000, 'no_reset', NULL, NULL, now(), NULL),
		       ('copy_waiting', 'on_hold', 1000, 1, 'no_reset', 'origin', 'origin-id', now(), now() + interval '1 minute'),
		       ('old_origin_over', 'active', 1000, 5000, 'no_reset', 'origin', NULL, now(), NULL)`)

	status := func(username string) string {
		t.Helper()
		var got string
		if err := h.pool.QueryRow(ctx, "SELECT status FROM users WHERE username=$1", username).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	runReview := func() {
		t.Helper()
		if err := review(ctx, h.q, testDispatcher(h.q), testLogger()); err != nil {
			t.Fatal(err)
		}
	}

	runReview()
	for username, want := range map[string]string{"copy_over": "active", "local_over": "limited", "copy_waiting": "on_hold", "old_origin_over": "limited"} {
		if got := status(username); got != want {
			t.Errorf("%s = %s, want %s", username, got, want)
		}
	}

	users, err := h.q.ResetDuePeriodicUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range users {
		if u.Username == "copy_over" {
			t.Error("a copy's counter was reset, losing traffic its origin hasn't read")
		}
	}

	// An expiry means the same anywhere, and has to work with the origin out of reach.
	h.execRaw(t, `UPDATE users SET expire = 1 WHERE username = 'copy_over'`)
	runReview()
	if got := status("copy_over"); got != "expired" {
		t.Errorf("past its expiry: %s, want expired", got)
	}
}
