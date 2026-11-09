package nodecore

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/legendary1205/rapido-go/internal/nodecore/spread"
)

// FallbackGroup describes one exit that must keep working when its WireGuard
// tunnel dies. Group is a selector outbound with two members: Primary, bound
// to Interface, and Fallback, the same exit over the default route. Routing
// rules point at Group and never notice which member is live.
//
// A direct Primary is a spread outbound: when the host has more tunnels for
// the same exit, named Interface-2, Interface-3 and so on, its users are shared
// among the healthy ones, and the exit only falls back once none is left.
type FallbackGroup struct {
	Group     string
	Primary   string
	Fallback  string
	Interface string
}

// Health answers whether a tunnel is usable right now.
type Health interface {
	Up(iface string) bool
}

// confirmedHealth is a Health that also tells a tunnel seen working apart from
// one never probed yet. Up gives the benefit of the doubt to an exit's own
// tunnel on a fresh start; a tunnel joining an exit gets users only once it has
// been seen to work.
type confirmedHealth interface {
	Confirmed(iface string) bool
}

// siblingSuffix matches what follows an exit's interface name in the name of
// one of its extra tunnels: a dash and a number from 2 to 99.
var siblingSuffix = regexp.MustCompile(`^-([2-9]|[1-9][0-9])$`)

// Siblings returns the names among tunnels that serve the same exit as iface:
// iface-2, iface-3 and so on, sorted as given.
func Siblings(iface string, tunnels []string) []string {
	var out []string
	for _, name := range tunnels {
		if rest, ok := strings.CutPrefix(name, iface); ok && siblingSuffix.MatchString(rest) {
			out = append(out, name)
		}
	}
	return out
}

func (n *Node) spreadOutbound(tag string) (*spread.Outbound, bool) {
	raw, loaded := n.box.Outbound().Outbound(tag)
	if !loaded {
		return nil, false
	}
	sp, ok := raw.(*spread.Outbound)
	return sp, ok
}

// SpreadMembers returns the tunnels a spread outbound shares its users among
// now, or nil when tag is not a spread outbound.
func (n *Node) SpreadMembers(tag string) []string {
	if sp, ok := n.spreadOutbound(tag); ok {
		return sp.Members()
	}
	return nil
}

type selectorGroup interface {
	SelectOutbound(tag string) bool
	Now() string
}

func (n *Node) selectorGroup(group string) (selectorGroup, error) {
	raw, loaded := n.box.Outbound().Outbound(group)
	if !loaded {
		return nil, fmt.Errorf("nodecore: no outbound tagged %q", group)
	}
	sel, ok := raw.(selectorGroup)
	if !ok {
		return nil, fmt.Errorf("nodecore: outbound %q is not a selector", group)
	}
	return sel, nil
}

// SelectOutbound points a selector outbound at one of its members.
func (n *Node) SelectOutbound(group, member string) error {
	sel, err := n.selectorGroup(group)
	if err != nil {
		return err
	}
	if !sel.SelectOutbound(member) {
		return fmt.Errorf("nodecore: selector %q has no member %q", group, member)
	}
	return nil
}

// SelectedOutbound returns the member a selector currently routes to.
func (n *Node) SelectedOutbound(group string) (string, error) {
	sel, err := n.selectorGroup(group)
	if err != nil {
		return "", err
	}
	return sel.Now(), nil
}

// Supervisor keeps every fallback group pointed at its Primary while the
// tunnel is healthy and at its Fallback while it is not.
type Supervisor struct {
	node   *Node
	groups []FallbackGroup
	health Health
	logger *slog.Logger

	// Foreign, when set, reports interfaces that do not belong to this host.
	// Every node is sent every exit in the fleet, so most groups here guard a
	// tunnel that lives on another server; those are switched silently, since
	// "tunnel is down" about a tunnel this host never had is noise.
	Foreign func(iface string) bool

	// Tunnels, when set, lists the host's tunnel interfaces; an exit's extra
	// tunnels are found among them (see Siblings). It is read once a round.
	Tunnels func() []string

	mu     sync.RWMutex
	active map[string]bool
}

func NewSupervisor(node *Node, groups []FallbackGroup, health Health, logger *slog.Logger) *Supervisor {
	if logger == nil {
		logger = slog.Default()
	}
	return &Supervisor{node: node, groups: groups, health: health, logger: logger, active: make(map[string]bool)}
}

// Reconcile makes one pass over every group.
func (s *Supervisor) Reconcile() {
	var tunnels []string
	if s.Tunnels != nil {
		tunnels = s.Tunnels()
	}
	for _, g := range s.groups {
		up := s.healthyTunnels(g.Interface, tunnels)
		s.spreadOver(g, up)
		want := g.Primary
		if len(up) == 0 {
			want = g.Fallback
		}
		current, err := s.node.SelectedOutbound(g.Group)
		if err != nil {
			s.logger.Error("fallback: read selection", "group", g.Group, "error", err)
			continue
		}
		if current != want {
			if err := s.node.SelectOutbound(g.Group, want); err != nil {
				s.logger.Error("fallback: switch", "group", g.Group, "to", want, "error", err)
				continue
			}
			if s.Foreign != nil && s.Foreign(g.Interface) {
				// nothing to report about someone else's tunnel
			} else if want == g.Fallback {
				s.logger.Warn("fallback: tunnel is down, using a direct connection", "exit", g.Group, "tunnel", g.Interface)
			} else {
				s.logger.Info("fallback: tunnel is back, using it again", "exit", g.Group, "tunnel", g.Interface)
			}
		}
		s.setActive(g.Interface, g.Group, want == g.Fallback)
	}
}

// healthyTunnels returns the exit's own tunnel when it is up, followed by
// those of its extra tunnels that have been seen working.
func (s *Supervisor) healthyTunnels(iface string, tunnels []string) []string {
	var up []string
	if s.health.Up(iface) {
		up = append(up, iface)
	}
	confirmed, strict := s.health.(confirmedHealth)
	for _, sibling := range Siblings(iface, tunnels) {
		if (strict && confirmed.Confirmed(sibling)) || (!strict && s.health.Up(sibling)) {
			up = append(up, sibling)
		}
	}
	return up
}

// spreadOver points a spread Primary at the healthy tunnels. With none it is
// left alone: the selector is on the fallback then, and the last set is what
// the exit comes back to.
func (s *Supervisor) spreadOver(g FallbackGroup, up []string) {
	sp, ok := s.node.spreadOutbound(g.Primary)
	if !ok || len(up) == 0 {
		return
	}
	changed, err := sp.SetMembers(up)
	if err != nil {
		s.logger.Error("fallback: spread exit over its tunnels", "exit", g.Group, "tunnels", strings.Join(up, ","), "error", err)
		return
	}
	if changed && !(s.Foreign != nil && s.Foreign(g.Interface)) {
		s.logger.Info("exit shared among its tunnels", "exit", g.Group, "tunnels", strings.Join(sp.Members(), ","))
	}
}

func (s *Supervisor) setActive(iface, group string, fallback bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// keyed by interface AND group: two exits may share a tunnel, and the
	// tunnel is on fallback while any of them is
	s.active[iface+"\x00"+group] = fallback
}

// ActiveByInterface reports which tunnels currently have at least one exit
// running over the fallback.
func (s *Supervisor) ActiveByInterface() map[string]bool {
	out := make(map[string]bool)
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, g := range s.groups {
		if s.active[g.Interface+"\x00"+g.Group] {
			out[g.Interface] = true
		}
	}
	return out
}

// Run reconciles immediately and then every interval until ctx is done.
func (s *Supervisor) Run(ctx context.Context, interval time.Duration) {
	s.Reconcile()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Reconcile()
		}
	}
}
