package txscope

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"sync"
)

type key struct{}
type poolKey struct{}
type state struct {
	tx    pgx.Tx
	mu    sync.Mutex
	after []func(context.Context)
}

func WithTransaction(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, key{}, &state{tx: tx})
}
func Tx(ctx context.Context) pgx.Tx {
	if s, ok := ctx.Value(key{}).(*state); ok {
		return s.tx
	}
	return nil
}
func AfterCommit(ctx context.Context, fn func(context.Context)) bool {
	if s, ok := ctx.Value(key{}).(*state); ok {
		s.mu.Lock()
		s.after = append(s.after, fn)
		s.mu.Unlock()
		return true
	}
	return false
}
func RunAfterCommit(ctx context.Context) {
	if s, ok := ctx.Value(key{}).(*state); ok {
		for _, fn := range s.after {
			fn(context.WithoutCancel(ctx))
		}
	}
}
func WithPool(ctx context.Context, pool *pgxpool.Pool) context.Context {
	return context.WithValue(ctx, poolKey{}, pool)
}
func Pool(ctx context.Context) *pgxpool.Pool { p, _ := ctx.Value(poolKey{}).(*pgxpool.Pool); return p }
