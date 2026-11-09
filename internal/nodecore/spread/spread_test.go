package spread

import (
	"context"
	"fmt"
	"io"
	"math"
	"net"
	"net/netip"
	"slices"
	"sync"
	"testing"

	"github.com/sagernet/sing-box/adapter"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/log"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"
)

func keys(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("user%05d", i)
	}
	return out
}

func TestPickSharesUsersEvenly(t *testing.T) {
	members := []string{"finland", "finland-2", "finland-3", "finland-4"}
	count := map[string]int{}
	users := keys(40000)
	for _, k := range users {
		count[Pick(members, k)]++
	}
	want := float64(len(users)) / float64(len(members))
	for _, m := range members {
		if dev := math.Abs(float64(count[m])-want) / want; dev > 0.04 {
			t.Errorf("%s got %d users, %.1f%% off an even share of %.0f", m, count[m], 100*dev, want)
		}
	}
}

func TestPickIsStableAndMovesOnlyWhatItMust(t *testing.T) {
	three := []string{"nl", "nl-2", "nl-3"}
	two := []string{"nl", "nl-3"}
	four := []string{"nl", "nl-2", "nl-3", "nl-4"}
	moved := 0
	for _, k := range keys(20000) {
		before := Pick(three, k)
		if again := Pick(slices.Clone(three), k); again != before {
			t.Fatalf("user %s: %s then %s for the same tunnels", k, before, again)
		}
		// A tunnel leaving moves only its own users.
		if after := Pick(two, k); before != "nl-2" && after != before {
			t.Fatalf("user %s moved from %s to %s although %s stayed", k, before, after, before)
		}
		// A tunnel joining only takes users, it never shuffles the others.
		if after := Pick(four, k); after != before {
			if after != "nl-4" {
				t.Fatalf("user %s moved from %s to %s when nl-4 joined", k, before, after)
			}
			moved++
		}
	}
	if share := float64(moved) / 20000; share < 0.22 || share > 0.28 {
		t.Errorf("the fourth tunnel took %.1f%% of users, want about 25%%", 100*share)
	}
	if Pick(nil, "anyone") != "" {
		t.Error("no members must pick nothing")
	}
}

// fakeMember dials an in-memory pipe and records which interface it is.
type fakeMember struct {
	outbound.Adapter
	iface string
	mu    sync.Mutex
	dials int
	peers []net.Conn
	stage []adapter.StartStage
	fail  error
}

func (f *fakeMember) Start(stage adapter.StartStage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stage = append(f.stage, stage)
	return nil
}

func (f *fakeMember) Close() error { return nil }

func (f *fakeMember) DialContext(ctx context.Context, network string, destination M.Socksaddr) (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return nil, f.fail
	}
	f.dials++
	a, b := net.Pipe()
	f.peers = append(f.peers, b)
	return a, nil
}

func (f *fakeMember) ListenPacket(ctx context.Context, destination M.Socksaddr) (net.PacketConn, error) {
	return nil, io.EOF
}

type rig struct {
	out     *Outbound
	mu      sync.Mutex
	members map[string]*fakeMember
}

func newRig(t *testing.T, primary string) *rig {
	t.Helper()
	r := &rig{members: map[string]*fakeMember{}}
	out, err := newOutbound("exit~wg", log.NewNOPFactory().Logger(), primary, func(iface string) (adapter.Outbound, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		m := &fakeMember{Adapter: outbound.NewAdapter("direct", "exit~wg", []string{N.NetworkTCP}, nil), iface: iface}
		r.members[iface] = m
		return m, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r.out = out
	return r
}

func (r *rig) member(iface string) *fakeMember {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.members[iface]
}

func asUser(user string) context.Context {
	return adapter.WithContext(context.Background(), &adapter.InboundContext{User: user})
}

func dial(t *testing.T, o *Outbound, ctx context.Context) net.Conn {
	t.Helper()
	conn, err := o.DialContext(ctx, N.NetworkTCP, M.ParseSocksaddr("1.2.3.4:443"))
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

func TestOneTunnelBehavesLikeABoundDirect(t *testing.T) {
	r := newRig(t, "germany")
	for _, u := range keys(50) {
		dial(t, r.out, asUser(u)).Close()
	}
	if got := r.member("germany").dials; got != 50 {
		t.Fatalf("dials through the only tunnel = %d, want 50", got)
	}
	if got := r.out.Members(); !slices.Equal(got, []string{"germany"}) {
		t.Fatalf("members = %v", got)
	}
}

func TestEachUserStaysOnOneTunnel(t *testing.T) {
	r := newRig(t, "finland")
	if changed, err := r.out.SetMembers([]string{"finland-3", "finland", "finland-2"}); err != nil || !changed {
		t.Fatalf("SetMembers = %v, %v", changed, err)
	}
	if got := r.out.Members(); !slices.Equal(got, []string{"finland", "finland-2", "finland-3"}) {
		t.Fatalf("members = %v, want sorted", got)
	}
	for _, u := range keys(300) {
		want := Pick(r.out.Members(), u)
		before := r.member(want).dials
		for range 3 {
			dial(t, r.out, asUser(u)).Close()
		}
		if got := r.member(want).dials - before; got != 3 {
			t.Fatalf("user %s: %d of 3 connections went through %s", u, got, want)
		}
	}
	for _, iface := range []string{"finland", "finland-2", "finland-3"} {
		if r.member(iface).dials < 200 {
			t.Errorf("%s carried %d of 900 connections; users are not being shared", iface, r.member(iface).dials)
		}
	}
}

func TestATunnelLeavingClosesOnlyItsConnections(t *testing.T) {
	r := newRig(t, "nl")
	if _, err := r.out.SetMembers([]string{"nl", "nl-2"}); err != nil {
		t.Fatal(err)
	}
	var onNL, onNL2 net.Conn
	for _, u := range keys(100) {
		c := dial(t, r.out, asUser(u))
		if Pick(r.out.Members(), u) == "nl" && onNL == nil {
			onNL = c
		} else if Pick(r.out.Members(), u) == "nl-2" && onNL2 == nil {
			onNL2 = c
		}
	}
	if changed, _ := r.out.SetMembers([]string{"nl"}); !changed {
		t.Fatal("dropping nl-2 must count as a change")
	}
	if _, err := onNL2.Write([]byte("x")); err == nil {
		t.Error("a connection through the tunnel that left must be closed")
	}
	go func() { _, _ = r.member("nl").peers[0].Read(make([]byte, 1)) }()
	if _, err := onNL.Write([]byte("x")); err != nil {
		t.Errorf("a connection through the tunnel that stayed must keep working: %v", err)
	}
	if changed, _ := r.out.SetMembers(nil); changed {
		t.Error("an empty set must leave the members alone")
	}
	if got := r.out.Members(); !slices.Equal(got, []string{"nl"}) {
		t.Errorf("members = %v after an empty set, want [nl]", got)
	}
	if changed, _ := r.out.SetMembers([]string{"nl", "nl"}); changed {
		t.Error("the same set again is no change")
	}
}

func TestAddressKeepsAClientWithoutAUserOnOneTunnel(t *testing.T) {
	r := newRig(t, "uk")
	if _, err := r.out.SetMembers([]string{"uk", "uk-2", "uk-3"}); err != nil {
		t.Fatal(err)
	}
	ctx := adapter.WithContext(context.Background(), &adapter.InboundContext{Source: M.SocksaddrFrom(netip.MustParseAddr("5.6.7.8"), 5000)})
	want := Pick(r.out.Members(), "5.6.7.8")
	for range 5 {
		dial(t, r.out, ctx).Close()
	}
	if got := r.member(want).dials; got != 5 {
		t.Errorf("%d of 5 connections from one address went through %s", got, want)
	}
	// No metadata at all still dials somewhere.
	dial(t, r.out, context.Background()).Close()
}

func TestATunnelAddedLaterIsStartedLikeTheOthers(t *testing.T) {
	r := newRig(t, "usa")
	for _, s := range []adapter.StartStage{adapter.StartStateInitialize, adapter.StartStateStart} {
		if err := r.out.Start(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.out.SetMembers([]string{"usa", "usa-2"}); err != nil {
		t.Fatal(err)
	}
	if got := r.member("usa-2").stage; !slices.Equal(got, []adapter.StartStage{adapter.StartStateInitialize, adapter.StartStateStart}) {
		t.Errorf("the late tunnel saw stages %v", got)
	}
}

func TestADialErrorNamesTheTunnel(t *testing.T) {
	r := newRig(t, "italy")
	r.member("italy").fail = fmt.Errorf("dial tcp 1.2.3.4:443: i/o timeout")
	_, err := r.out.DialContext(asUser("a"), N.NetworkTCP, M.ParseSocksaddr("1.2.3.4:443"))
	if err == nil || err.Error() != "italy: dial tcp 1.2.3.4:443: i/o timeout" {
		t.Errorf("error = %v", err)
	}
}

func TestMissingInterfaceIsRefused(t *testing.T) {
	if _, err := newOutbound("x", log.NewNOPFactory().Logger(), "", nil); err == nil {
		t.Error("a spread outbound without an interface must be refused")
	}
}
