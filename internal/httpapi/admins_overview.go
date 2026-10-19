package httpapi

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/legendary1205/rapido-go/internal/auth"
	"github.com/legendary1205/rapido-go/internal/db/generated"
)

// noOwnerName labels the users nobody owns (those of the bootstrap sudo
// account, and Gateway replicas), which have no admins row.
const noOwnerName = "(no owner)"

type configCountsDTO struct {
	Total    int64 `json:"total"`
	Active   int64 `json:"active"`
	Disabled int64 `json:"disabled"`
	Limited  int64 `json:"limited"`
	Expired  int64 `json:"expired"`
	OnHold   int64 `json:"on_hold"`
}

type topConsumerDTO struct {
	Username string `json:"username"`
	Used     int64  `json:"used_traffic"`
	// Limit is null for a config without a data limit.
	Limit *int64 `json:"data_limit"`
}

// adminOverviewDTO is one row of GET /api/admins/overview: one admin and a
// summary of the configs they own.
type adminOverviewDTO struct {
	// AdminID is null on the row of users nobody owns.
	AdminID  *int32 `json:"admin_id"`
	Username string `json:"username"`
	IsSudo   bool   `json:"is_sudo"`

	Configs configCountsDTO `json:"configs"`
	// UsedTraffic is the traffic all the admin's configs used; DataLimit the
	// sum of the data limits of those that have one, UnlimitedConfigs how many
	// have none.
	UsedTraffic      int64 `json:"used_traffic"`
	DataLimit        int64 `json:"data_limit"`
	UnlimitedConfigs int64 `json:"unlimited_configs"`
	// Active configs that expire within 3 / 7 days, and active configs with a
	// data limit that have 10% or less of it left.
	ExpiringIn3Days int64 `json:"expiring_in_3_days"`
	ExpiringIn7Days int64 `json:"expiring_in_7_days"`
	LowData         int64 `json:"low_data"`
	// Online is how many of the admin's configs are connected right now.
	Online       int64            `json:"online"`
	TopConsumers []topConsumerDTO `json:"top_consumers"`
}

// overviewWindows are the unix-second edges of the expiry windows.
func overviewWindows(now time.Time) (nowTS, in3, in7 int32) {
	return int32(now.Unix()), int32(now.Add(72 * time.Hour).Unix()), int32(now.Add(7 * 24 * time.Hour).Unix())
}

// buildAdminOverview assembles the rows. admins are the admins to report (an
// admin with no configs gets an all-zero row); a counts/online/top entry whose
// admin is NULL lands on the "no owner" row, shown only when it has configs and
// includeNoOwner is set.
func buildAdminOverview(admins []generated.Admin, counts []generated.AdminOverviewCountsRow, top []generated.AdminTopConsumersRow, online map[int32]int64, includeNoOwner bool) []adminOverviewDTO {
	countByAdmin := map[int32]generated.AdminOverviewCountsRow{}
	var noOwner *generated.AdminOverviewCountsRow
	for i, c := range counts {
		if c.AdminID.Valid {
			countByAdmin[c.AdminID.Int32] = c
		} else {
			noOwner = &counts[i]
		}
	}
	topByAdmin := map[int32][]topConsumerDTO{}
	for _, t := range top {
		key := int32(0)
		if t.AdminID.Valid {
			key = t.AdminID.Int32
		}
		topByAdmin[key] = append(topByAdmin[key], topConsumerDTO{Username: t.Username, Used: t.UsedTraffic, Limit: int8ToPtr(t.DataLimit)})
	}
	fill := func(dto *adminOverviewDTO, c generated.AdminOverviewCountsRow, key int32) {
		dto.Configs = configCountsDTO{Total: c.Total, Active: c.Active, Disabled: c.Disabled, Limited: c.Limited, Expired: c.Expired, OnHold: c.OnHold}
		dto.UsedTraffic, dto.DataLimit, dto.UnlimitedConfigs = c.UsedTraffic, c.DataLimit, c.Unlimited
		dto.ExpiringIn3Days, dto.ExpiringIn7Days, dto.LowData = c.Expiring3d, c.Expiring7d, c.LowData
		dto.Online = online[key]
		dto.TopConsumers = topByAdmin[key]
		if dto.TopConsumers == nil {
			dto.TopConsumers = []topConsumerDTO{}
		}
	}

	out := make([]adminOverviewDTO, 0, len(admins)+1)
	for _, a := range admins {
		id := a.ID
		dto := adminOverviewDTO{AdminID: &id, Username: a.Username, IsSudo: a.IsSudo}
		fill(&dto, countByAdmin[a.ID], a.ID)
		out = append(out, dto)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	if includeNoOwner && noOwner != nil && noOwner.Total > 0 {
		dto := adminOverviewDTO{Username: noOwnerName}
		fill(&dto, *noOwner, 0)
		out = append(out, dto)
	}
	return out
}

func sumOverview(rows []adminOverviewDTO) adminOverviewDTO {
	t := adminOverviewDTO{Username: "total", TopConsumers: []topConsumerDTO{}}
	for _, r := range rows {
		t.Configs.Total += r.Configs.Total
		t.Configs.Active += r.Configs.Active
		t.Configs.Disabled += r.Configs.Disabled
		t.Configs.Limited += r.Configs.Limited
		t.Configs.Expired += r.Configs.Expired
		t.Configs.OnHold += r.Configs.OnHold
		t.UsedTraffic += r.UsedTraffic
		t.DataLimit += r.DataLimit
		t.UnlimitedConfigs += r.UnlimitedConfigs
		t.ExpiringIn3Days += r.ExpiringIn3Days
		t.ExpiringIn7Days += r.ExpiringIn7Days
		t.LowData += r.LowData
		t.Online += r.Online
	}
	return t
}

// onlineByAdmin counts the connected configs per owning admin (key 0 for
// configs nobody owns): from live presence when every node reports it, from the
// database's online_at otherwise.
func (h *Handler) onlineByAdmin(ctx context.Context) (map[int32]int64, error) {
	now := time.Now()
	out := map[int32]int64{}
	add := func(admin pgtype.Int4, n int64) {
		key := int32(0)
		if admin.Valid {
			key = admin.Int32
		}
		out[key] += n
	}

	cutoff := strconv.FormatInt(now.Add(-presenceOnlineWindow).UnixMilli(), 10)
	pipe := h.store.Cache.Raw().Pipeline()
	live := h.queuePresenceLive(ctx, pipe)
	names := pipe.ZRangeByScore(ctx, presenceUsersKey, &redis.ZRangeBy{Min: cutoff, Max: "+inf"})
	if _, err := pipe.Exec(ctx); err == nil && live.authoritative() {
		if len(names.Val()) == 0 {
			return out, nil
		}
		rows, err := h.store.Queries.CountUsersByUsernamesPerAdmin(ctx, names.Val())
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			add(r.AdminID, r.Online)
		}
		return out, nil
	}
	rows, err := h.store.Queries.CountOnlineUsersPerAdminSince(ctx, timestamptzFromTime(now.UTC().Add(-onlineWindow)))
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		add(r.AdminID, r.Online)
	}
	return out, nil
}

// handleAdminsOverview implements GET /api/admins/overview: one row per admin
// with the counts of their configs by status, used traffic and data limits,
// configs about to expire or run out of data, how many are online and their top
// five consumers. A sudo admin sees every admin (plus a row for configs nobody
// owns); a reseller gets only their own row.
func (h *Handler) handleAdminsOverview(c *gin.Context) {
	identity := auth.CurrentIdentity(c)
	ctx := c.Request.Context()

	var scope pgtype.Int4
	var admins []generated.Admin
	var err error
	if identity.IsSudo {
		admins, err = h.store.Queries.ListAdmins(ctx, generated.ListAdminsParams{})
	} else {
		var a generated.Admin
		a, err = h.store.Queries.GetAdminByID(ctx, identity.AdminID)
		admins = []generated.Admin{a}
		scope = pgInt4FromInt(int(identity.AdminID))
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read admins"})
		return
	}

	nowTS, in3, in7 := overviewWindows(time.Now())
	counts, err := h.store.Queries.AdminOverviewCounts(ctx, generated.AdminOverviewCountsParams{Now: nowTS, In3: in3, In7: in7, AdminID: scope})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not summarise configs"})
		return
	}
	top, err := h.store.Queries.AdminTopConsumers(ctx, scope)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read the top consumers"})
		return
	}
	online, err := h.onlineByAdmin(ctx)
	if err != nil {
		h.logger.Warn("admins overview: could not count online configs", "error", err)
		online = map[int32]int64{}
	}
	rows := buildAdminOverview(admins, counts, top, online, identity.IsSudo)
	c.JSON(http.StatusOK, gin.H{"generated_at": time.Now().UTC(), "admins": rows, "totals": sumOverview(rows)})
}
