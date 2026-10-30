package traffic

import (
	"context"
	"github.com/legendary1205/rapido-go/internal/nodecore/userkey"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	N "github.com/sagernet/sing/common/network"
)

// ConnGroup is every client connection one core generation has accepted and
// not yet finished with, so that closing the core can end them all.
//
// sing-box closes a core by stopping its listeners and its router, and the
// router's connection manager then drops the connections it is relaying. A
// multiplexed session is not one of those: its carrier connection is read by the
// inbound's own goroutine, which keeps accepting new streams from the client and
// routing each one into the closed router, where it fails ("dns router closed").
// The client sees a healthy connection that answers every request with an
// error, and nothing makes it reconnect. Closing the carrier connection is what
// ends the session, and only the inbound knows it - hence this group, filled by
// the inbounds and emptied by Node.Close.
//
// One group belongs to exactly one core generation (see nodecore.New), unlike
// the Manager, which lives as long as the process. A connection whose
// handshake finishes after its core was closed is therefore refused at once
// instead of joining the next generation.
//
// A nil *ConnGroup is valid and tracks nothing.
type ConnGroup struct {
	mu     sync.Mutex
	conns  map[uint64]io.Closer
	next   uint64
	closed bool
	// users is by inbound tag: a user change visits one listener's connections at
	// a time, and with many listeners, scanning everybody's for each of them adds
	// up under this lock, which every new connection also needs.
	users    map[string]map[uint64]connectionUser
	policies map[string]map[string][32]byte
	lists    map[reflect.Type]any
}

func NewConnGroup() *ConnGroup {
	return &ConnGroup{conns: make(map[uint64]io.Closer)}
}

type connGroupKey struct{}

// closeWorkers bounds how many connections CloseAll closes at once, and
// closeWait how long it waits for them: closing a TLS connection may block on a
// peer that has stopped reading, and neither one such peer nor many may hold up
// a core restart. Closes still running when the wait ends carry on in the
// background.
const closeWorkers = 64

var closeWait = 3 * time.Second

// Hold registers c and returns the func that forgets it again. The func is
// idempotent. If the group is already closed, c is closed instead and the func
// does nothing.
func (g *ConnGroup) Hold(c io.Closer) (release func()) {
	if g == nil || c == nil {
		return func() {}
	}
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		c.Close()
		return func() {}
	}
	g.next++
	id := g.next
	g.conns[id] = c
	g.mu.Unlock()

	var released atomic.Bool
	return func() {
		if !released.CompareAndSwap(false, true) {
			return
		}
		g.mu.Lock()
		delete(g.conns, id)
		g.mu.Unlock()
	}
}

// Track is Hold for an inbound handler: it returns onClose with the forgetting
// chained in front of it. Hand the result to the router in place of onClose.
// onClose may be nil.
func (g *ConnGroup) Track(c io.Closer, onClose N.CloseHandlerFunc) N.CloseHandlerFunc {
	if g == nil {
		return onClose
	}
	release := g.Hold(c)
	return func(err error) {
		release()
		if onClose != nil {
			onClose(err)
		}
	}
}

// Len is how many connections the group holds right now.
func (g *ConnGroup) Len() int {
	if g == nil {
		return 0
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.conns)
}

// CloseAll closes every connection the group holds and refuses any later one.
// It returns once they are all closed, or after closeWait if some are stuck.
// Calling it again is harmless.
func (g *ConnGroup) CloseAll() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.closed = true
	all := make([]io.Closer, 0, len(g.conns))
	for _, c := range g.conns {
		all = append(all, c)
	}
	clear(g.conns)
	clear(g.users)
	clear(g.policies)
	clear(g.lists)
	g.mu.Unlock()

	closeConnections(all)
}

func closeConnections(all []io.Closer) {
	work := make(chan io.Closer)
	var wg sync.WaitGroup
	for range min(closeWorkers, len(all)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range work {
				c.Close()
			}
		}()
	}
	go func() {
		for _, c := range all {
			work <- c
		}
		close(work)
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeWait):
	}
}

// WithConnGroup and ConnGroupFromContext thread a core generation's group to
// its inbounds the same way NewContext does the Manager.
func WithConnGroup(ctx context.Context, g *ConnGroup) context.Context {
	return context.WithValue(ctx, connGroupKey{}, g)
}

func ConnGroupFromContext(ctx context.Context) *ConnGroup {
	g, _ := ctx.Value(connGroupKey{}).(*ConnGroup)
	return g
}

// connectionUser binds a carrier or packet stream to its authenticated account.
type connectionUser struct {
	tag, name   string
	fingerprint [32]byte
}

// SharedLists is this generation's memo of T's user lists, so that listeners
// with the same users share what those users build to. Kept per generation, so
// nothing built here can outlive the core, or the logger, it was built with. A
// nil group shares nothing.
func SharedLists[T comparable, V any](g *ConnGroup) *userkey.Lists[T, V] {
	if g == nil {
		return &userkey.Lists[T, V]{}
	}
	key := reflect.TypeFor[userkey.Lists[T, V]]()
	g.mu.Lock()
	defer g.mu.Unlock()
	if lists, ok := g.lists[key]; ok {
		return lists.(*userkey.Lists[T, V])
	}
	lists := &userkey.Lists[T, V]{}
	if g.lists == nil {
		g.lists = map[reflect.Type]any{}
	}
	g.lists[key] = lists
	return lists
}

// SharedList is the userkey.List for users, built once per generation.
func SharedList[T comparable](g *ConnGroup, users []T) *userkey.List {
	return SharedLists[T, *userkey.List](g).Get(users, func() *userkey.List { return userkey.NewList(users) })
}

// SetUserPolicy closes only removed accounts or changed credentials. Unchanged
// users keep their streams even if the option list is reordered.
func SetUserPolicy[T any](g *ConnGroup, tag string, users []T) {
	if g == nil {
		return
	}
	SetPolicy(g, tag, userkey.NewList(users).Policy)
}

// SetPolicy is SetUserPolicy for a list already built. The group keeps policy
// without copying it, so the caller must not change it afterwards.
func SetPolicy(g *ConnGroup, tag string, policy map[string][32]byte) {
	if g == nil {
		return
	}
	g.mu.Lock()
	if g.policies == nil {
		g.policies = map[string]map[string][32]byte{}
	}
	g.policies[tag] = policy
	var removed []io.Closer
	for id, identity := range g.users[tag] {
		fp, ok := policy[identity.name]
		if !ok || fp != identity.fingerprint {
			if c := g.conns[id]; c != nil {
				removed = append(removed, c)
			}
			delete(g.conns, id)
			delete(g.users[tag], id)
		}
	}
	g.mu.Unlock()
	closeConnections(removed)
}

func (g *ConnGroup) HoldUser(tag, name string, c io.Closer) func() {
	return g.holdAuthenticated(tag, name, nil, c)
}

func (g *ConnGroup) HoldAuthenticated(tag, name string, fingerprint [32]byte, c io.Closer) func() {
	return g.holdAuthenticated(tag, name, &fingerprint, c)
}

func (g *ConnGroup) holdAuthenticated(tag, name string, authenticated *[32]byte, c io.Closer) func() {
	if g == nil {
		return func() {}
	}
	g.mu.Lock()
	policy, restricted := g.policies[tag]
	fp, allowed := policy[name]
	if g.closed || (restricted && (!allowed || authenticated != nil && fp != *authenticated)) {
		g.mu.Unlock()
		c.Close()
		return func() {}
	}
	g.next++
	id := g.next
	g.conns[id] = c
	if g.users == nil {
		g.users = map[string]map[uint64]connectionUser{}
	}
	if g.users[tag] == nil {
		g.users[tag] = map[uint64]connectionUser{}
	}
	g.users[tag][id] = connectionUser{tag, name, fp}
	g.mu.Unlock()
	var released atomic.Bool
	return func() {
		if released.CompareAndSwap(false, true) {
			g.mu.Lock()
			delete(g.conns, id)
			delete(g.users[tag], id)
			g.mu.Unlock()
		}
	}
}

func (g *ConnGroup) TrackAuthenticated(tag, name string, fingerprint [32]byte, c io.Closer, onClose N.CloseHandlerFunc) N.CloseHandlerFunc {
	release := g.HoldAuthenticated(tag, name, fingerprint, c)
	return func(err error) {
		release()
		if onClose != nil {
			onClose(err)
		}
	}
}

func (g *ConnGroup) TrackUser(tag, name string, c io.Closer, onClose N.CloseHandlerFunc) N.CloseHandlerFunc {
	release := g.HoldUser(tag, name, c)
	return func(err error) {
		release()
		if onClose != nil {
			onClose(err)
		}
	}
}
