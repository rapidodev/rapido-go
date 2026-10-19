package httpapi

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/legendary1205/rapido-go/internal/auth"
	"github.com/legendary1205/rapido-go/internal/db/generated"
	"github.com/legendary1205/rapido-go/internal/integrationsettings"
	"github.com/legendary1205/rapido-go/internal/resellerapi"
	"github.com/legendary1205/rapido-go/internal/subscription"
)

// ---- shared filter

// extractSubscriptionToken pulls a subscription token out of what an admin
// pasted into the search box: a full link (".../sub/<token>", possibly with a
// format suffix or a query string) or the bare token. It returns "" when the
// text does not look like either.
func extractSubscriptionToken(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 2000 {
		return ""
	}
	if i := strings.Index(s, "/sub/"); i >= 0 {
		rest := s[i+len("/sub/"):]
		if cut := strings.IndexAny(rest, "/?#"); cut >= 0 {
			rest = rest[:cut]
		}
		return rest
	}
	if len(s) < 15 || strings.ContainsAny(s, " /:?#\t\n") {
		return ""
	}
	return s
}

// userListFilter builds the filter GET /api/users, its CSV export and the bulk
// endpoint share: a non-sudo admin is always limited to their own configs, sudo
// may narrow to one admin by username; search matches the username, the note,
// a config UID, and - when it is a pasted subscription link or token - the user
// that token names. matchable is false when the filter can match nothing (an
// unknown admin name), so the caller answers with an empty list rather than
// "everyone".
func (h *Handler) userListFilter(ctx context.Context, identity *auth.Identity, adminName, search string, statuses []string) (params generated.ListUsersParams, matchable bool) {
	if !identity.IsSudo {
		params.AdminID = pgInt4FromInt(int(identity.AdminID))
	} else if adminName != "" {
		a, err := h.store.Queries.GetAdminByUsername(ctx, adminName)
		if err != nil {
			return params, false
		}
		params.AdminID = pgInt4FromInt(int(a.ID))
	}
	if search != "" {
		params.Search = textFromPtr(&search)
		if token := extractSubscriptionToken(search); token != "" {
			if claims, ok := subscription.ParseToken(token, h.jwtSecret); ok {
				username := claims.Username
				if claims.ByID {
					if u, err := h.store.Queries.GetUserByID(ctx, int32(claims.UserID)); err == nil {
						username = u.Username
					}
				}
				if username != "" {
					params.SearchUsername = textFromPtr(&username)
				}
			}
		}
	}
	if len(statuses) > 0 {
		params.Statuses = statuses
	}
	return params, true
}

// ---- bulk actions

// Bulk actions.
const (
	bulkEnable      = "enable"
	bulkDisable     = "disable"
	bulkExtendDays  = "extend_days"
	bulkResetUsage  = "reset_usage"
	bulkDelete      = "delete"
	maxBulkUsers    = 500
	maxBulkExtendBy = 3650
)

var validBulkActions = map[string]bool{bulkEnable: true, bulkDisable: true, bulkExtendDays: true, bulkResetUsage: true, bulkDelete: true}

type bulkFilter struct {
	Admin  string   `json:"admin"`
	Status []string `json:"status"`
	Search string   `json:"search"`
}

type bulkRequest struct {
	// Usernames is an explicit list; Filter selects users the way
	// GET /api/users does. Exactly one of them is given.
	Usernames []string    `json:"usernames"`
	Filter    *bulkFilter `json:"filter"`
	// Action is enable, disable, extend_days, reset_usage or delete.
	Action string `json:"action" binding:"required"`
	// Days is required by extend_days (1 to 3650).
	Days *int `json:"days"`
}

// Per-user outcomes of a bulk call.
const (
	bulkOK        = "ok"
	bulkSkipped   = "skipped"
	bulkFailed    = "error"
	bulkForbidden = "forbidden"
	bulkNotFound  = "not_found"
)

type bulkResultDTO struct {
	Username string `json:"username"`
	// Status is ok, skipped (nothing to change), error, forbidden (not your
	// user) or not_found.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type bulkResponse struct {
	Action    string          `json:"action"`
	Total     int             `json:"total"`
	Succeeded int             `json:"succeeded"`
	Skipped   int             `json:"skipped"`
	Failed    int             `json:"failed"`
	Results   []bulkResultDTO `json:"results"`
}

// bulkPlan is what an action does to one user, decided without touching the
// database.
type bulkPlan struct {
	// Skip, when set, is why nothing is done.
	Skip string
	// NewStatus and NewExpire are the values to write (for enable, disable and
	// extend_days).
	NewStatus string
	NewExpire pgtype.Int4
	// Err is a reason the action cannot be applied at all.
	Err string
}

func dataLimitReached(u generated.User) bool {
	return u.DataLimit.Valid && u.DataLimit.Int64 > 0 && u.UsedTraffic >= u.DataLimit.Int64
}

// planBulk decides what enable, disable and extend_days do to a user. It is
// pure so the rules are tested without a database; reset_usage and delete have
// nothing to plan.
//
//   - enable turns a disabled user back on; a disabled user whose data is used
//     up or whose expiry has passed is not turned "active" into a state the
//     next usage tick would revert, so it is skipped with the reason.
//   - disable turns anything but a disabled user off.
//   - extend_days moves the expiry forward by days from the later of the
//     current expiry and now, and brings an expired user back (to limited if
//     their data is used up).
func planBulk(u generated.User, action string, days int, now time.Time) bulkPlan {
	switch action {
	case bulkEnable:
		switch {
		case u.Status == statusActive:
			return bulkPlan{Skip: "already active"}
		case u.Status != statusDisabled:
			return bulkPlan{Skip: "status is " + u.Status + ", only disabled users can be enabled"}
		case dataLimitReached(u):
			return bulkPlan{Skip: "the data limit is used up; reset the usage or raise the limit first"}
		case u.Expire.Valid && int64(u.Expire.Int32) <= now.Unix():
			return bulkPlan{Skip: "the expiry has passed; extend it first"}
		}
		return bulkPlan{NewStatus: statusActive, NewExpire: u.Expire}
	case bulkDisable:
		if u.Status == statusDisabled {
			return bulkPlan{Skip: "already disabled"}
		}
		return bulkPlan{NewStatus: statusDisabled, NewExpire: u.Expire}
	case bulkExtendDays:
		if u.Status == statusOnHold {
			return bulkPlan{Skip: "the user is on hold (no expiry yet)"}
		}
		if !u.Expire.Valid {
			return bulkPlan{Skip: "the user has no expiry (unlimited)"}
		}
		base := int64(u.Expire.Int32)
		if base < now.Unix() {
			base = now.Unix()
		}
		newExpire := base + int64(days)*86400
		if !expireFitsColumn(&newExpire) {
			return bulkPlan{Err: expireOutOfRangeDetail}
		}
		status := u.Status
		if status == statusExpired {
			status = statusActive
			if dataLimitReached(u) {
				status = statusLimited
			}
		}
		return bulkPlan{NewStatus: status, NewExpire: pgtype.Int4{Int32: int32(newExpire), Valid: true}}
	}
	return bulkPlan{Err: "unknown action"}
}

// resellerUserLimit asks the reseller API for an admin's active-user cap; nil
// means no cap (not configured, unreachable, or uncapped).
func (h *Handler) resellerUserLimit(ctx context.Context, adminUsername string) *int {
	row, err := h.store.CachedGetIntegrationSettings(ctx)
	if err != nil {
		return nil
	}
	vals := integrationsettings.Resolve(row, h.envDefaults)
	return h.resellerapi.GetUsersLimit(ctx, resellerapi.Config{Secret: vals.ResellerApiSecret, URL: vals.ResellerApiUrl}, adminUsername)
}

// applyUserChange writes a new status and expiry for a user the way
// PUT /api/user/:username does - same query, same reports, same Gateway push.
func (h *Handler) applyUserChange(ctx context.Context, identity *auth.Identity, dbuser generated.User, newStatus string, newExpire pgtype.Int4) error {
	updated, err := h.store.Queries.UpdateUserCore(ctx, generated.UpdateUserCoreParams{
		ID: dbuser.ID, Status: newStatus, DataLimit: dbuser.DataLimit, DataLimitResetStrategy: dbuser.DataLimitResetStrategy,
		Expire: newExpire, Note: dbuser.Note, OnHoldExpireDuration: dbuser.OnHoldExpireDuration,
		OnHoldTimeout: dbuser.OnHoldTimeout, AutoDeleteInDays: dbuser.AutoDeleteInDays,
	})
	if err != nil {
		return fmt.Errorf("could not update the user")
	}
	if resp, err := h.buildUserResponse(ctx, updated); err == nil {
		userAdmin := h.resolveAdminRef(ctx, updated.AdminID)
		h.reports.UserUpdated(ctx, toUserSummary(resp), identity.Username, userAdmin)
		if updated.Status != dbuser.Status {
			h.reports.StatusChange(ctx, updated.Username, updated.Status, userAdmin)
		}
	}
	h.dispatchGatewayUserSync(ctx, updated)
	return nil
}

// handleBulkUsers implements POST /api/users/bulk: one action on many users,
// chosen by an explicit list or by a filter. Every user goes through the same
// code as the single-user endpoints (reports, Gateway sync, node data), and a
// non-sudo admin can only touch their own users - others come back
// "forbidden" in the per-user results, not as a failure of the whole call.
func (h *Handler) handleBulkUsers(c *gin.Context) {
	identity := auth.CurrentIdentity(c)
	var req bulkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": err.Error()})
		return
	}
	if !validBulkActions[req.Action] {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "action must be one of enable, disable, extend_days, reset_usage, delete"})
		return
	}
	days := 0
	if req.Action == bulkExtendDays {
		if req.Days == nil || *req.Days < 1 || *req.Days > maxBulkExtendBy {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "extend_days needs days between 1 and 3650"})
			return
		}
		days = *req.Days
	}
	if (len(req.Usernames) == 0) == (req.Filter == nil) {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": "give exactly one of usernames or filter"})
		return
	}
	ctx := c.Request.Context()

	var users []generated.User
	var results []bulkResultDTO
	if req.Filter != nil {
		params, matchable := h.userListFilter(ctx, identity, req.Filter.Admin, req.Filter.Search, req.Filter.Status)
		if matchable {
			params.Limit = pgInt4FromInt(maxBulkUsers + 1)
			sort := validUserSort("")
			params.Sort = textFromPtr(&sort)
			rows, err := h.store.Queries.ListUsers(ctx, params)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not list users"})
				return
			}
			if len(rows) > maxBulkUsers {
				c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": fmt.Sprintf("the filter matches more than %d users; narrow it", maxBulkUsers)})
				return
			}
			users = rows
		}
	} else {
		if len(req.Usernames) > maxBulkUsers {
			c.JSON(http.StatusUnprocessableEntity, gin.H{"detail": fmt.Sprintf("at most %d usernames per call", maxBulkUsers)})
			return
		}
		seen := map[string]bool{}
		var names []string
		for _, n := range req.Usernames {
			if n = strings.TrimSpace(n); n != "" && !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
		rows, err := h.store.Queries.ListUsersByUsernames(ctx, names)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"detail": "Could not read users"})
			return
		}
		found := map[string]generated.User{}
		for _, u := range rows {
			found[u.Username] = u
		}
		for _, n := range names {
			u, ok := found[n]
			switch {
			case !ok:
				results = append(results, bulkResultDTO{Username: n, Status: bulkNotFound, Detail: "User not found"})
			case !identity.IsSudo && (!u.AdminID.Valid || u.AdminID.Int32 != identity.AdminID):
				results = append(results, bulkResultDTO{Username: n, Status: bulkForbidden, Detail: "You're not allowed"})
			default:
				users = append(users, u)
			}
		}
	}

	// A reseller reactivating users stays under the cap the reseller API sets.
	budget := -1
	if req.Action == bulkEnable && !identity.IsSudo {
		if limit := h.resellerUserLimit(ctx, identity.Username); limit != nil {
			count, err := h.store.Queries.CountUsers(ctx, generated.CountUsersParams{
				AdminID: pgInt4FromInt(int(identity.AdminID)), Statuses: []string{statusActive, statusOnHold},
			})
			if err == nil {
				budget = *limit - int(count)
				if budget < 0 {
					budget = 0
				}
			}
		}
	}

	now := time.Now().UTC()
	for _, u := range users {
		results = append(results, h.bulkOne(ctx, identity, u, req.Action, days, now, &budget))
	}
	resp := bulkResponse{Action: req.Action, Total: len(results), Results: results}
	if resp.Results == nil {
		resp.Results = []bulkResultDTO{}
	}
	for _, r := range results {
		switch r.Status {
		case bulkOK:
			resp.Succeeded++
		case bulkSkipped:
			resp.Skipped++
		default:
			resp.Failed++
		}
	}
	c.JSON(http.StatusOK, resp)
}

func (h *Handler) bulkOne(ctx context.Context, identity *auth.Identity, u generated.User, action string, days int, now time.Time, budget *int) bulkResultDTO {
	res := bulkResultDTO{Username: u.Username}
	if u.SyncedFromPanelName.Valid {
		res.Status, res.Detail = bulkSkipped, "managed by another panel ("+u.SyncedFromPanelName.String+") via the Gateway"
		return res
	}
	switch action {
	case bulkResetUsage:
		if _, err := h.resetUsageCore(ctx, u, identity.Username); err != nil {
			res.Status, res.Detail = bulkFailed, err.Error()
			return res
		}
	case bulkDelete:
		if err := h.deleteUserCore(ctx, u, identity.Username); err != nil {
			res.Status, res.Detail = bulkFailed, err.Error()
			return res
		}
	default:
		plan := planBulk(u, action, days, now)
		switch {
		case plan.Err != "":
			res.Status, res.Detail = bulkFailed, plan.Err
			return res
		case plan.Skip != "":
			res.Status, res.Detail = bulkSkipped, plan.Skip
			return res
		}
		if action == bulkEnable && *budget >= 0 {
			if *budget == 0 {
				res.Status, res.Detail = bulkFailed, "User limit reached. Please contact your administrator."
				return res
			}
			*budget--
		}
		if err := h.applyUserChange(ctx, identity, u, plan.NewStatus, plan.NewExpire); err != nil {
			res.Status, res.Detail = bulkFailed, err.Error()
			return res
		}
	}
	res.Status = bulkOK
	return res
}

// ---- CSV export

var csvHeader = []string{"username", "status", "used_traffic", "data_limit", "expire", "admin", "created_at", "note", "online"}

// csvSafe neutralises a cell a spreadsheet would run as a formula.
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// userCSVRow is one export row.
func userCSVRow(u generated.User, adminName string, online bool) []string {
	limit, expire := "", ""
	if u.DataLimit.Valid {
		limit = fmt.Sprint(u.DataLimit.Int64)
	}
	if u.Expire.Valid {
		expire = time.Unix(int64(u.Expire.Int32), 0).UTC().Format(time.RFC3339)
	}
	created := ""
	if u.CreatedAt.Valid {
		created = u.CreatedAt.Time.UTC().Format(time.RFC3339)
	}
	note := ""
	if u.Note.Valid {
		note = u.Note.String
	}
	return []string{
		csvSafe(u.Username), u.Status, fmt.Sprint(u.UsedTraffic), limit, expire, csvSafe(adminName), created, csvSafe(note), fmt.Sprint(online),
	}
}

// exportPageSize is how many users the export reads at a time.
const exportPageSize = 500

// handleExportUsers implements GET /api/users/export: the users GET /api/users
// would list for the same ?admin= / ?search= / ?status= / ?sort= (no paging)
// as a streamed CSV with the columns username, status, used_traffic (bytes),
// data_limit (bytes, empty = unlimited), expire (RFC 3339 UTC, empty = none),
// admin, created_at, note, online.
func (h *Handler) handleExportUsers(c *gin.Context) {
	identity := auth.CurrentIdentity(c)
	ctx := c.Request.Context()
	params, matchable := h.userListFilter(ctx, identity, c.Query("admin"), c.Query("search"), c.QueryArray("status"))
	sort := validUserSort(c.Query("sort"))
	params.Sort = textFromPtr(&sort)

	admins := map[int32]string{}
	if rows, err := h.store.Queries.ListAdmins(ctx, generated.ListAdminsParams{}); err == nil {
		for _, a := range rows {
			admins[a.ID] = a.Username
		}
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="users-`+time.Now().UTC().Format("20060102-1504")+`.csv"`)
	c.Status(http.StatusOK)
	w := c.Writer
	_, _ = w.Write([]byte("\xEF\xBB\xBF")) // byte order mark: spreadsheets read the UTF-8 notes correctly
	cw := csv.NewWriter(w)
	_ = cw.Write(csvHeader)
	if !matchable {
		cw.Flush()
		return
	}

	for offset := 0; ; offset += exportPageSize {
		params.Limit, params.Offset = pgInt4FromInt(exportPageSize), pgInt4FromInt(offset)
		rows, err := h.store.Queries.ListUsers(ctx, params)
		if err != nil {
			h.logger.Warn("users export: page failed", "offset", offset, "error", err)
			break
		}
		names := make([]string, len(rows))
		for i, u := range rows {
			names[i] = u.Username
		}
		presence := h.loadPresence(ctx, names)
		now := time.Now()
		for _, u := range rows {
			online, _ := presence.userStatus(u.Username, u.OnlineAt, now)
			admin := ""
			if u.AdminID.Valid {
				admin = admins[u.AdminID.Int32]
			}
			_ = cw.Write(userCSVRow(u, admin, online))
		}
		cw.Flush()
		w.Flush()
		if len(rows) < exportPageSize || ctx.Err() != nil {
			break
		}
	}
	cw.Flush()
}
