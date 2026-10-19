package httpapi

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/subscription"
)

func TestExtractSubscriptionToken(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	tok := subscription.CreateToken("alice_vpn", secret)
	cases := map[string]string{
		"https://panel.example.com/sub/" + tok:                tok,
		"https://panel.example.com:8443/sub/" + tok + "/info": tok,
		"http://x/sub/" + tok + "?flag=1":                     tok,
		"  " + tok + "  ":                                     tok,
		"/sub/" + tok + "#frag":                               tok,
		"alice":                                               "",
		"hello world this is a note":                          "",
		"":                                                    "",
		"short-token":                                         "",
	}
	for in, want := range cases {
		if got := extractSubscriptionToken(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
	// the extracted token decodes to the username it was made for
	claims, ok := subscription.ParseToken(extractSubscriptionToken("https://p/sub/"+tok), secret)
	if !ok || claims.Username != "alice_vpn" {
		t.Errorf("claims = %+v ok=%v", claims, ok)
	}
	// a token signed with another secret does not decode
	if _, ok := subscription.ParseToken(tok, []byte("another-secret-another-secret-xx")); ok {
		t.Error("foreign token accepted")
	}
	_ = base64.StdEncoding
}

func user(status string, used int64, limit *int64, expire *int32) generated.User {
	u := generated.User{Username: "u", Status: status, UsedTraffic: used}
	if limit != nil {
		u.DataLimit = pgtype.Int8{Int64: *limit, Valid: true}
	}
	if expire != nil {
		u.Expire = pgtype.Int4{Int32: *expire, Valid: true}
	}
	return u
}

func i64(v int64) *int64 { return &v }
func i32(v int32) *int32 { return &v }

func TestPlanBulkEnable(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	future, past := i32(int32(now.Unix())+86400), i32(int32(now.Unix())-86400)
	cases := []struct {
		name string
		u    generated.User
		skip bool
		want string
	}{
		{"disabled with room is enabled", user("disabled", 10, i64(100), future), false, "active"},
		{"disabled unlimited, no expiry", user("disabled", 10, nil, nil), false, "active"},
		{"already active", user("active", 0, nil, nil), true, ""},
		{"limited is not enabled", user("limited", 100, i64(100), future), true, ""},
		{"disabled but data used up", user("disabled", 100, i64(100), future), true, ""},
		{"disabled but expired", user("disabled", 1, i64(100), past), true, ""},
		{"on hold is not enabled", user("on_hold", 0, nil, nil), true, ""},
	}
	for _, c := range cases {
		p := planBulk(c.u, bulkEnable, 0, now)
		if (p.Skip != "") != c.skip || p.NewStatus != c.want || p.Err != "" {
			t.Errorf("%s: %+v", c.name, p)
		}
	}
}

func TestPlanBulkDisable(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	if p := planBulk(user("active", 0, nil, nil), bulkDisable, 0, now); p.NewStatus != "disabled" || p.Skip != "" {
		t.Errorf("%+v", p)
	}
	if p := planBulk(user("limited", 0, nil, nil), bulkDisable, 0, now); p.NewStatus != "disabled" {
		t.Errorf("a limited user can be disabled: %+v", p)
	}
	if p := planBulk(user("disabled", 0, nil, nil), bulkDisable, 0, now); p.Skip == "" {
		t.Errorf("already disabled: %+v", p)
	}
}

func TestPlanBulkExtendDays(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	future := int32(now.Unix()) + 10*86400
	past := int32(now.Unix()) - 5*86400

	p := planBulk(user("active", 0, nil, i32(future)), bulkExtendDays, 30, now)
	if p.NewExpire.Int32 != future+30*86400 || p.NewStatus != "active" {
		t.Errorf("extends from the current expiry: %+v", p)
	}
	p = planBulk(user("expired", 0, i64(100), i32(past)), bulkExtendDays, 30, now)
	if p.NewExpire.Int32 != int32(now.Unix())+30*86400 || p.NewStatus != "active" {
		t.Errorf("an expired user is extended from now and reactivated: %+v", p)
	}
	p = planBulk(user("expired", 100, i64(100), i32(past)), bulkExtendDays, 30, now)
	if p.NewStatus != "limited" {
		t.Errorf("an expired user whose data is used up comes back limited: %+v", p)
	}
	if p = planBulk(user("disabled", 0, nil, i32(future)), bulkExtendDays, 7, now); p.NewStatus != "disabled" || p.NewExpire.Int32 != future+7*86400 {
		t.Errorf("a disabled user stays disabled: %+v", p)
	}
	if p = planBulk(user("active", 0, nil, nil), bulkExtendDays, 7, now); p.Skip == "" {
		t.Errorf("no expiry: %+v", p)
	}
	if p = planBulk(user("on_hold", 0, nil, nil), bulkExtendDays, 7, now); p.Skip == "" {
		t.Errorf("on hold: %+v", p)
	}
	if p = planBulk(user("active", 0, nil, i32(2_147_000_000)), bulkExtendDays, 3650, now); p.Err == "" {
		t.Errorf("an expiry past 2038 does not fit the column: %+v", p)
	}
}

func TestUserCSVRowAndFormulaGuard(t *testing.T) {
	u := generated.User{
		Username: "alice", Status: "active", UsedTraffic: 123,
		DataLimit: pgtype.Int8{Int64: 1000, Valid: true}, Expire: pgtype.Int4{Int32: 1_800_000_000, Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Valid: true},
		Note:      pgtype.Text{String: "=HYPERLINK(\"http://evil\")", Valid: true},
	}
	row := userCSVRow(u, "reseller1", true)
	if len(row) != len(csvHeader) {
		t.Fatalf("columns: %d vs %d", len(row), len(csvHeader))
	}
	want := []string{"alice", "active", "123", "1000", "2027-01-15T08:00:00Z", "reseller1", "2026-01-02T03:04:05Z", "'=HYPERLINK(\"http://evil\")", "true"}
	for i := range want {
		if row[i] != want[i] {
			t.Errorf("column %s = %q, want %q", csvHeader[i], row[i], want[i])
		}
	}
	unlimited := userCSVRow(generated.User{Username: "bob", Status: "disabled"}, "", false)
	if unlimited[3] != "" || unlimited[4] != "" || unlimited[5] != "" || unlimited[8] != "false" {
		t.Errorf("unlimited row = %v", unlimited)
	}
	if csvSafe("-5") != "'-5" || csvSafe("normal") != "normal" || csvSafe("") != "" {
		t.Error("csvSafe")
	}
}

func TestBuildAdminOverview(t *testing.T) {
	admins := []generated.Admin{{ID: 2, Username: "zed"}, {ID: 1, Username: "amir", IsSudo: true}, {ID: 3, Username: "empty"}}
	counts := []generated.AdminOverviewCountsRow{
		{AdminID: pgtype.Int4{Int32: 1, Valid: true}, Total: 10, Active: 6, Disabled: 1, Limited: 1, Expired: 1, OnHold: 1, UsedTraffic: 500, DataLimit: 900, Unlimited: 2, Expiring3d: 2, Expiring7d: 4, LowData: 3},
		{AdminID: pgtype.Int4{Int32: 2, Valid: true}, Total: 3, Active: 3, UsedTraffic: 50},
		{Total: 4, Active: 4, UsedTraffic: 7},
	}
	top := []generated.AdminTopConsumersRow{
		{AdminID: pgtype.Int4{Int32: 1, Valid: true}, Username: "big", UsedTraffic: 300, DataLimit: pgtype.Int8{Int64: 400, Valid: true}},
		{AdminID: pgtype.Int4{Int32: 1, Valid: true}, Username: "unlim", UsedTraffic: 100},
		{Username: "orphan", UsedTraffic: 7},
	}
	online := map[int32]int64{1: 5, 0: 1}

	rows := buildAdminOverview(admins, counts, top, online, true)
	if len(rows) != 4 {
		t.Fatalf("rows = %d", len(rows))
	}
	if rows[0].Username != "amir" || rows[1].Username != "empty" || rows[2].Username != "zed" || rows[3].Username != noOwnerName {
		t.Errorf("order = %s %s %s %s", rows[0].Username, rows[1].Username, rows[2].Username, rows[3].Username)
	}
	a := rows[0]
	if a.Configs.Total != 10 || a.Configs.OnHold != 1 || a.ExpiringIn3Days != 2 || a.ExpiringIn7Days != 4 || a.LowData != 3 || a.Online != 5 || !a.IsSudo || a.UnlimitedConfigs != 2 {
		t.Errorf("amir = %+v", a)
	}
	if len(a.TopConsumers) != 2 || a.TopConsumers[0].Username != "big" || *a.TopConsumers[0].Limit != 400 || a.TopConsumers[1].Limit != nil {
		t.Errorf("top = %+v", a.TopConsumers)
	}
	if rows[1].Configs.Total != 0 || rows[1].TopConsumers == nil || len(rows[1].TopConsumers) != 0 || rows[1].AdminID == nil {
		t.Errorf("an admin with no configs gets an all-zero row: %+v", rows[1])
	}
	if rows[3].AdminID != nil || rows[3].Online != 1 || rows[3].TopConsumers[0].Username != "orphan" {
		t.Errorf("no-owner row = %+v", rows[3])
	}
	// a reseller sees only their own row, never the no-owner one
	own := buildAdminOverview(admins[:1], counts[1:2], nil, online, false)
	if len(own) != 1 || own[0].Username != "zed" || own[0].Configs.Total != 3 {
		t.Errorf("own = %+v", own)
	}
	if tot := sumOverview(rows); tot.Configs.Total != 17 || tot.UsedTraffic != 557 || tot.Online != 6 || tot.LowData != 3 {
		t.Errorf("totals = %+v", tot)
	}
	// the no-owner row is left out when there is nothing in it
	if got := buildAdminOverview(admins, counts[:2], top, online, true); len(got) != 3 {
		t.Errorf("rows without orphans = %d", len(got))
	}
}

func TestOverviewWindows(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	n, in3, in7 := overviewWindows(now)
	if int64(n) != 1_800_000_000 || in3-n != 3*86400 || in7-n != 7*86400 {
		t.Errorf("%d %d %d", n, in3, in7)
	}
}
