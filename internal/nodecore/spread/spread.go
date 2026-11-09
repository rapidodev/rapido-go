// Package spread is an outbound that shares one exit among several network
// interfaces: a location served by more than one tunnel, so that its users are
// not all squeezed through a single one.
//
// Each user is kept on one interface by rendezvous hashing of their name over
// the interfaces that are healthy right now. All of a user's connections leave
// from the same address, and a change in the set only moves the users it has
// to: the ones on an interface that went away, or the share an added
// interface takes. Every interface is dialled by sing-box's own direct
// outbound, bound to it, so an exit behaves exactly as a plain bound direct
// outbound does when it has one interface.
package spread

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"net"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/common/interrupt"
	"github.com/sagernet/sing-box/log"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/direct"
	E "github.com/sagernet/sing/common/exceptions"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

// Type is the outbound type this package registers.
const Type = "rapido-spread"

// Options configures a spread outbound.
type Options struct {
	// Direct is the direct outbound every interface is dialled with. Its own
	// BindInterface is ignored: each member binds to its interface.
	Direct option.DirectOutboundOptions `json:"direct"`
	// Interface is the exit's own interface, the only member until SetMembers
	// names others.
	Interface string `json:"interface"`
}

// Register adds the spread outbound to registry.
func Register(registry *outbound.Registry) {
	outbound.Register[Options](registry, Type, New)
}

// memberFactory builds the outbound that dials through one interface.
type memberFactory func(iface string) (adapter.Outbound, error)

type member struct {
	out   adapter.Outbound
	conns *interrupt.Group
}

// Outbound is a spread outbound.
type Outbound struct {
	outbound.Adapter
	logger  log.ContextLogger
	primary string
	build   memberFactory

	mu      sync.Mutex
	members map[string]*member
	started []adapter.StartStage

	current atomic.Pointer[[]string]
}

// New builds a spread outbound; sing-box's registry calls it.
func New(ctx context.Context, router adapter.Router, logger log.ContextLogger, tag string, options Options) (adapter.Outbound, error) {
	base := options.Direct
	return newOutbound(tag, logger, options.Interface, func(iface string) (adapter.Outbound, error) {
		opts := base
		opts.BindInterface = iface
		return direct.NewOutbound(ctx, router, logger, tag, opts)
	})
}

func newOutbound(tag string, logger log.ContextLogger, primary string, build memberFactory) (*Outbound, error) {
	if primary == "" {
		return nil, E.New("spread: missing interface")
	}
	o := &Outbound{
		Adapter: outbound.NewAdapter(Type, tag, []string{N.NetworkTCP, N.NetworkUDP}, nil),
		logger:  logger,
		primary: primary,
		build:   build,
		members: make(map[string]*member),
	}
	// Built now so a broken option fails the config, not the first connection.
	if _, err := o.member(primary); err != nil {
		return nil, err
	}
	o.current.Store(&[]string{primary})
	return o, nil
}

// Start passes each start stage on to the members, and remembers it so that a
// member built later can be brought to the same stage.
func (o *Outbound) Start(stage adapter.StartStage) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = append(o.started, stage)
	for _, m := range o.members {
		if err := adapter.LegacyStart(m.out, stage); err != nil {
			return err
		}
	}
	return nil
}

// Close closes the members and every connection made through them.
func (o *Outbound) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	var errs []error
	for _, m := range o.members {
		m.conns.Interrupt(true)
		if c, ok := m.out.(interface{ Close() error }); ok {
			errs = append(errs, c.Close())
		}
	}
	return E.Errors(errs...)
}

func (o *Outbound) member(iface string) (*member, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if m := o.members[iface]; m != nil {
		return m, nil
	}
	out, err := o.build(iface)
	if err != nil {
		return nil, E.Cause(err, "spread: interface ", iface)
	}
	for _, stage := range o.started {
		if err := adapter.LegacyStart(out, stage); err != nil {
			return nil, E.Cause(err, "spread: start interface ", iface)
		}
	}
	m := &member{out: out, conns: interrupt.NewGroup()}
	o.members[iface] = m
	return m, nil
}

// Members returns the interfaces new connections are spread over now.
func (o *Outbound) Members() []string {
	return slices.Clone(*o.current.Load())
}

// SetMembers replaces the interfaces new connections are spread over and
// reports whether that changed anything. An empty list leaves the members as
// they are: with nothing healthy the exit's fallback takes over, and the old
// members are what it comes back to. Connections still open through an
// interface that left the set are closed, so their clients reconnect through
// one that works instead of waiting out their own timeouts.
func (o *Outbound) SetMembers(ifaces []string) (bool, error) {
	next := slices.Clone(ifaces)
	slices.Sort(next)
	next = slices.Compact(next)
	if len(next) == 0 {
		return false, nil
	}
	for _, iface := range next {
		if _, err := o.member(iface); err != nil {
			return false, err
		}
	}
	prev := *o.current.Swap(&next)
	if slices.Equal(prev, next) {
		return false, nil
	}
	o.mu.Lock()
	for _, iface := range prev {
		if !slices.Contains(next, iface) {
			o.members[iface].conns.Interrupt(true)
		}
	}
	o.mu.Unlock()
	return true, nil
}

// pick chooses the member for the connection ctx belongs to.
func (o *Outbound) pick(ctx context.Context) (string, *member, error) {
	key := ""
	if metadata := adapter.ContextFrom(ctx); metadata != nil {
		key = metadata.User
		if key == "" && metadata.Source.IsValid() {
			// Without a user name the client's address keeps one client on
			// one interface.
			key = metadata.Source.Addr.String()
		}
	}
	iface := Pick(*o.current.Load(), key)
	m, err := o.member(iface)
	return iface, m, err
}

func (o *Outbound) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	iface, m, err := o.pick(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := m.out.DialContext(ctx, network, destination)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", iface, err)
	}
	return m.conns.NewConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

func (o *Outbound) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	iface, m, err := o.pick(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := m.out.ListenPacket(ctx, destination)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", iface, err)
	}
	return m.conns.NewPacketConn(conn, interrupt.IsExternalConnectionFromContext(ctx)), nil
}

// Pick returns the member key is kept on: the one with the highest score for
// key (rendezvous hashing). Each member's share of keys is even, and removing
// a member moves only the keys it had. It returns "" for no members.
func Pick(members []string, key string) string {
	best, bestScore := "", uint64(0)
	for _, m := range members {
		if s := score(key, m); best == "" || s > bestScore || (s == bestScore && m < best) {
			best, bestScore = m, s
		}
	}
	return best
}

func score(key, member string) uint64 {
	h := fnv.New64a()
	h.Write([]byte(key))
	h.Write([]byte{0})
	h.Write([]byte(member))
	// FNV alone spreads short, similar names unevenly; a final mix fixes that.
	return mix(binary.BigEndian.Uint64(h.Sum(nil)))
}

// mix is splitmix64's finaliser.
func mix(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
