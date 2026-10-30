package httpapi

import (
	"bytes"
	"context"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/legendary1205/rapido-go/internal/subscription"
	"github.com/legendary1205/rapido-go/internal/txscope"
	"net/http"
	"sort"
	"strings"
)

type requestDB struct{ pool *pgxpool.Pool }

func (d requestDB) db(ctx context.Context) generatedDB {
	if tx, ok := func() (pgx.Tx, bool) { t := txscope.Tx(ctx); return t, t != nil }(); ok {
		return tx
	}
	return d.pool
}

type generatedDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (d requestDB) Exec(c context.Context, q string, a ...any) (pgconn.CommandTag, error) {
	return d.db(c).Exec(c, q, a...)
}
func (d requestDB) Query(c context.Context, q string, a ...any) (pgx.Rows, error) {
	return d.db(c).Query(c, q, a...)
}
func (d requestDB) QueryRow(c context.Context, q string, a ...any) pgx.Row {
	return d.db(c).QueryRow(c, q, a...)
}

type atomicWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
	code int
}

func (w *atomicWriter) WriteHeader(code int)              { w.code = code }
func (w *atomicWriter) WriteHeaderNow()                   {}
func (w *atomicWriter) Write(b []byte) (int, error)       { return w.body.Write(b) }
func (w *atomicWriter) WriteString(b string) (int, error) { return w.body.WriteString(b) }
func (w *atomicWriter) Status() int                       { return w.code }
func (w *atomicWriter) Size() int                         { return w.body.Len() }
func (w *atomicWriter) Written() bool                     { return w.body.Len() > 0 }
func needsAtomicMutation(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions {
		return false
	}
	if path == "/api/admin/token" {
		return false
	}
	// Minutes on an SSH session, which no database transaction should wait on: it
	// writes nothing but its own log line, and by the time a transaction would
	// commit, the request's budget is long gone and the answer would be a 503 for
	// a restart that worked.
	if isNodeRestartPath(path) {
		return false
	}
	for _, p := range []string{"/api/user", "/api/admin", "/api/hosts", "/api/node", "/api/inbounds", "/api/tickets", "/api/internal/gateway/users/sync", "/api/settings/core-config", "/api/settings/xray-config", "/api/core/config"} {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return strings.HasPrefix(path, "/sub/")
}

// Responses are not released until commit succeeds, including multi-step user
// edits. A handler failure or panic rolls back every query using this context.
func atomicMutations(h *Handler) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !needsAtomicMutation(c.Request.Method, c.Request.URL.Path) {
			c.Next()
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, "/api/") && !strings.HasPrefix(c.Request.URL.Path, "/api/internal/") {
			if _, err := h.issuer.Verify(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")); err != nil {
				c.AbortWithStatusJSON(401, gin.H{"detail": "Could not validate credentials"})
				return
			}
		}
		tx, err := h.store.Pool.Begin(c.Request.Context())
		if err != nil {
			c.AbortWithStatusJSON(503, gin.H{"detail": "Database unavailable"})
			return
		}
		defer tx.Rollback(context.Background())
		ctx := txscope.WithTransaction(c.Request.Context(), tx)
		if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock_shared(814720033)"); err != nil {
			c.AbortWithStatusJSON(503, gin.H{"detail": "Restore in progress"})
			return
		}
		// Serializing conflicting mutations makes quota checks, ticket caps and
		// next-plan decisions atomic across API workers, not just within a process.
		keys := []string{}
		path := c.Request.URL.Path
		// Taking the fleet lock and a snapshot is only for who the route will let
		// through: otherwise any reseller could keep every config change waiting by
		// sending requests that end in a 403 anyway.
		structural := false
		if claims, err := h.issuer.Verify(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")); err == nil {
			structural = claims.IsSudo() && structuralConfigMutation(c.Request.Method, path)
			keys = append(keys, "admin:"+claims.Username)
		}
		parts := strings.Split(strings.Trim(path, "/"), "/")
		if len(parts) >= 3 && parts[0] == "api" && parts[1] == "user" {
			keys = append(keys, "user:"+parts[2])
		}
		if len(parts) >= 3 && parts[0] == "api" && parts[1] == "tickets" {
			keys = append(keys, "ticket:"+parts[2])
		}
		if len(parts) >= 2 && parts[0] == "sub" {
			if claims, ok := subscription.ParseToken(parts[1], h.signingSecret()); ok {
				name := claims.Username
				if claims.ByID {
					if user, err := h.store.Queries.GetUserByID(ctx, int32(claims.UserID)); err == nil {
						name = user.Username
					}
				}
				keys = append(keys, "user:"+name)
			}
			if len(parts) >= 4 && parts[2] == "tickets" {
				keys = append(keys, "ticket:"+parts[3])
			}
		}
		if len(keys) == 0 {
			keys = append(keys, "settings:"+path)
		}
		if structural {
			keys = append(keys, "fleet-config")
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", key); err != nil {
				c.AbortWithStatusJSON(503, gin.H{"detail": "Could not lock mutation"})
				return
			}
		}
		if structural {
			if err := h.saveFleetSnapshot(ctx, c.Request.Method+" "+path); err != nil {
				c.AbortWithStatusJSON(503, gin.H{"detail": "Snapshot failed; configuration unchanged"})
				return
			}
		}
		c.Request = c.Request.WithContext(ctx)
		original := c.Writer
		buffered := &atomicWriter{ResponseWriter: original, code: http.StatusOK}
		c.Writer = buffered
		defer func() { c.Writer = original }()
		c.Next()
		if buffered.code < 400 {
			if err = tx.Commit(ctx); err != nil {
				original.WriteHeader(503)
				_, _ = original.Write([]byte(`{"detail":"Operation was not committed; reload before retrying"}`))
				return
			}
			txscope.RunAfterCommit(ctx)
		} else {
			_ = tx.Rollback(context.Background())
		}
		original.WriteHeader(buffered.code)
		_, _ = original.Write(buffered.body.Bytes())
	}
}
