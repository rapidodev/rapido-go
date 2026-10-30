package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/legendary1205/rapido-go/internal/proxysettings"
)

func TestSplitUsableUsersLeavesOutOnlyTheOffender(t *testing.T) {
	good1 := userSpec{Name: "a", UUID: "8f8a4c1e-1e2a-4b8a-9b1a-000000000001"}
	good2 := userSpec{Name: "b", UUID: "8f8a4c1e-1e2a-4b8a-9b1a-000000000002"}
	cases := []struct {
		name string
		bad  userSpec
	}{
		{"shared credential", userSpec{Name: "x", UUID: good1.UUID}},
		{"shared name", userSpec{Name: "a", UUID: "8f8a4c1e-1e2a-4b8a-9b1a-000000000003"}},
		{"no credential", userSpec{Name: "x"}},
		{"not a uuid", userSpec{Name: "x", UUID: "not-a-uuid"}},
		{"unknown flow", userSpec{Name: "x", UUID: "8f8a4c1e-1e2a-4b8a-9b1a-000000000003", Flow: "xtls-rprx-direct"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kept, problems := splitUsableUsers("vless", []userSpec{good1, tc.bad, good2})
			if len(kept) != 2 || kept[0] != good1 || kept[1] != good2 {
				t.Errorf("kept = %+v, want the two good users", kept)
			}
			if len(problems) != 1 {
				t.Errorf("problems = %q, want one", problems)
			}
			// The credential itself must never reach the log this ends up in.
			if tc.bad.UUID != "" && strings.Contains(strings.Join(problems, " "), tc.bad.UUID) {
				t.Errorf("problem names the credential: %q", problems)
			}
		})
	}
}

func TestSplitUsableUsersReturnsACleanListItself(t *testing.T) {
	users := []userSpec{{Name: "a", UUID: "8f8a4c1e-1e2a-4b8a-9b1a-000000000001"}}
	kept, problems := splitUsableUsers("vless", users)
	if &kept[0] != &users[0] || problems != nil {
		t.Errorf("a clean list came back as a copy or with problems: %q", problems)
	}
}

func TestSplitUsableUsersTUICNeedsAPassword(t *testing.T) {
	kept, _ := splitUsableUsers("tuic", []userSpec{{Name: "a", UUID: "8f8a4c1e-1e2a-4b8a-9b1a-000000000001"}})
	if len(kept) != 0 {
		t.Errorf("a tuic user with no password was kept: %+v", kept)
	}
}

// Every cipher the panel accepts for a user has to be one a node can run: one it
// can't refuses the whole inbound.
func TestEveryCipherThePanelAcceptsRunsOnANode(t *testing.T) {
	for _, method := range proxysettings.ShadowsocksMethods {
		if err := checkShadowsocksMethod(string(method)); err != nil {
			t.Errorf("%s: %v", method, err)
		}
	}
}

func TestCheckPortsDistinctRefusesASharedSocket(t *testing.T) {
	vless := func(tag string, port uint16) inboundSpec {
		return inboundSpec{Tag: tag, Protocol: "vless", ListenPort: port}
	}
	if err := checkPortsDistinct([]inboundSpec{vless("a", 443), vless("b", 443)}); err == nil {
		t.Error("two TCP inbounds on one port were accepted")
	}
	// Different sockets, despite the same number.
	hy2 := inboundSpec{Tag: "c", Protocol: "hysteria2", ListenPort: 443}
	if err := checkPortsDistinct([]inboundSpec{vless("a", 443), hy2}); err != nil {
		t.Errorf("TCP and UDP on one port: %v", err)
	}
	ss := inboundSpec{Tag: "d", Protocol: "shadowsocks", ListenPort: 443}
	if err := checkPortsDistinct([]inboundSpec{hy2, ss}); err == nil {
		t.Error("shadowsocks opens UDP too, yet shared a port with hysteria2")
	}
}

func TestCheckPortsFreeFindsATakenPortButSparesTheRunningCores(t *testing.T) {
	holder, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	port := uint16(holder.Addr().(*net.TCPAddr).Port)
	in := []inboundSpec{{Tag: "a", Protocol: "vless", ListenPort: port}}
	if err := checkPortsFree(in, nil); err == nil {
		t.Error("a port another process holds passed")
	}
	if err := checkPortsFree(in, in); err != nil {
		t.Errorf("a port the running core holds failed: %v", err)
	}
}

func TestConfigRetryDelayDoublesUpToAnHour(t *testing.T) {
	want := []time.Duration{5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour}
	for i, delay := range want {
		if got := configRetryDelay(i + 1); got != delay {
			t.Errorf("failure %d: delay %s, want %s", i+1, got, delay)
		}
	}
}

func TestConfigShapeDigestIgnoresOnlyUsers(t *testing.T) {
	base := pulledConfig{Inbounds: []inboundSpec{{Tag: "a", Protocol: "trojan", ListenPort: 1, Users: []userSpec{{Name: "x"}}}}}
	otherUsers := pulledConfig{Inbounds: []inboundSpec{{Tag: "a", Protocol: "trojan", ListenPort: 1, Users: []userSpec{{Name: "y"}}}}}
	otherPort := pulledConfig{Inbounds: []inboundSpec{{Tag: "a", Protocol: "trojan", ListenPort: 2, Users: []userSpec{{Name: "x"}}}}}
	if configShapeDigest(base) != configShapeDigest(otherUsers) {
		t.Error("a user change alone looks like a new config")
	}
	if configShapeDigest(base) == configShapeDigest(otherPort) {
		t.Error("a port change looks like the same config")
	}
	if len(base.Inbounds[0].Users) != 1 {
		t.Error("the digest emptied the caller's user list")
	}
}

// One user's bad row must cost that user, not every other user's change.
func TestPullOnceAppliesAUserListWithABadEntry(t *testing.T) {
	echoHost, echoPort := startEcho(t)
	port := freeTCPPort(t)
	f := protoFixtures()[1]
	alice, bob := f.user("alice", 1), f.user("bob", 2)
	clash := userSpec{Name: "carol", Password: alice.Password}
	panel := &fakePanel{etags: true, cfg: pulledConfig{Version: "v1", Inbounds: []inboundSpec{f.inbound("main", []uint16{port}, alice)}}}
	pull, srv := pullFrom(t, panel)
	pull()
	node := srv.node

	panel.set(pulledConfig{Version: "v2", Inbounds: []inboundSpec{f.inbound("main", []uint16{port}, alice, clash, bob)}})
	pull()
	if srv.node != node {
		t.Error("the update restarted the node")
	}
	if srv.lastPulled.Version != "v2" {
		t.Errorf("lastPulled = %q, want v2", srv.lastPulled.Version)
	}
	if err := f.echoProxied(port, bob, echoHost, echoPort); err != nil {
		t.Errorf("bob, in the same update as the bad row: %v", err)
	}
	if err := f.echoProxied(port, alice, echoHost, echoPort); err != nil {
		t.Errorf("alice: %v", err)
	}
}

// A config that can't start: nobody gets disconnected for it, again and again,
// and users still come and go on what keeps running.
func TestPullOnceRestsAConfigThatCannotRunAndKeepsApplyingUsers(t *testing.T) {
	echoHost, echoPort := startEcho(t)
	port := freeTCPPort(t)
	f := protoFixtures()[1]
	alice, bob, carol := f.user("alice", 1), f.user("bob", 2), f.user("carol", 3)

	holder, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	taken := uint16(holder.Addr().(*net.TCPAddr).Port)
	defer holder.Close()

	panel := &fakePanel{etags: true, cfg: pulledConfig{Version: "v1", Inbounds: []inboundSpec{f.inbound("main", []uint16{port}, alice)}}}
	pull, srv := pullFrom(t, panel)
	srv.stateDir = t.TempDir()
	pull()
	node := srv.node
	if node == nil {
		t.Fatal("the first pull did not start the node")
	}

	second := f.inbound("second", []uint16{taken}, alice)
	panel.set(pulledConfig{Version: "v2", Inbounds: []inboundSpec{f.inbound("main", []uint16{port}, alice, bob), second}})
	pull()
	if srv.node != node {
		t.Fatal("a config that cannot run stopped the running core")
	}
	if !strings.Contains(srv.configError(), "second") {
		t.Errorf("configError = %q, want it to name what could not start", srv.configError())
	}
	if err := f.echoProxied(port, bob, echoHost, echoPort); err != nil {
		t.Errorf("bob arrived with the rejected config and must still get in: %v", err)
	}

	// Further users, same broken shape: nothing restarts, the users still apply,
	// and the removed one is out.
	panel.set(pulledConfig{Version: "v3", Inbounds: []inboundSpec{f.inbound("main", []uint16{port}, alice, carol), second}})
	pull()
	if srv.node != node {
		t.Fatal("the rejected shape was tried again before its rest was over")
	}
	if err := f.echoProxied(port, carol, echoHost, echoPort); err != nil {
		t.Errorf("carol: %v", err)
	}
	if err := f.echoProxied(port, bob, echoHost, echoPort); err == nil {
		t.Error("bob was removed and still got in")
	}

	// Nothing changes on the panel, but the rest ends and the port is free now.
	holder.Close()
	srv.mu.Lock()
	srv.rejected.retryAt = time.Now().Add(-time.Second)
	srv.mu.Unlock()
	pull()
	if srv.node == node {
		t.Fatal("the rest ended and the config was not tried again")
	}
	if srv.configError() != "" {
		t.Errorf("configError = %q after the config ran", srv.configError())
	}
	if err := f.echoProxied(taken, carol, echoHost, echoPort); err == nil {
		t.Error("carol is not on the second inbound and still got in")
	}
	if err := f.echoProxied(taken, alice, echoHost, echoPort); err != nil {
		t.Errorf("alice on the second inbound: %v", err)
	}
}

// statusPanel answers every pull with one status; ok serves an empty config.
type statusPanel struct{ status atomic.Int32 }

func (p *statusPanel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(int(p.status.Load()))
}

func TestOneRefusalFromThePanelDoesNotStopTheCore(t *testing.T) {
	f := protoFixtures()[1]
	port := freeTCPPort(t)
	echoHost, echoPort := startEcho(t)
	alice := f.user("alice", 1)
	srv := newTestServer(t)
	srv.stateDir = t.TempDir()
	cfg := pulledConfig{Version: "v1", Inbounds: []inboundSpec{f.inbound("main", []uint16{port}, alice)}}
	if err := srv.applyFullLocked(cfg); err != nil {
		t.Fatal(err)
	}
	node := srv.node

	panel := &statusPanel{}
	ts := httptest.NewServer(panel)
	defer ts.Close()
	pull := func() {
		srv.pullOnce(context.Background(), ts.Client(), config{PanelURL: ts.URL, ReportSecret: "secret"})
	}

	// What comes from something between here and the panel says nothing.
	for _, status := range []int{http.StatusForbidden, http.StatusBadGateway, http.StatusServiceUnavailable} {
		panel.status.Store(int32(status))
		pull()
		if srv.node != node || !srv.refusedSince.IsZero() {
			t.Fatalf("a %d touched the core or started the refusal clock", status)
		}
	}

	panel.status.Store(http.StatusUnauthorized)
	pull()
	if srv.node != node {
		t.Fatal("one 401 stopped the core")
	}
	if srv.refusedSince.IsZero() {
		t.Fatal("the 401 did not start the clock")
	}
	if err := f.echoProxied(port, alice, echoHost, echoPort); err != nil {
		t.Fatalf("alice during the grace period: %v", err)
	}

	// The panel accepting the secret again ends it.
	panel.status.Store(http.StatusNotModified)
	pull()
	if !srv.refusedSince.IsZero() {
		t.Error("the panel accepted the secret and the clock kept running")
	}

	// Refused for the whole grace period: the node was really removed.
	panel.status.Store(http.StatusUnauthorized)
	pull()
	srv.refusedSince = time.Now().Add(-panelRevocationGrace - time.Second)
	pull()
	if srv.node != nil {
		t.Error("still serving after the panel refused it for the whole grace period")
	}
	if srv.lastPulled != nil {
		t.Error("lastPulled survived the removal")
	}
	if _, err := os.Stat(filepath.Join(srv.stateDir, "last-good-config.json")); !os.IsNotExist(err) {
		t.Error("a boot would serve the removed node's users again")
	}
	if _, err := os.Stat(filepath.Join(srv.stateDir, "last-good-config.revoked.json")); err != nil {
		t.Errorf("the last config was not kept aside: %v", err)
	}
}

// Nothing runs and the panel can't be reached: the node starts again by itself
// once what stopped it is gone. An operator's stop stays a stop.
func TestCoreComesBackOnItsOwnAfterAFailedStart(t *testing.T) {
	f := protoFixtures()[1]
	port := freeTCPPort(t)
	alice := f.user("alice", 1)
	srv := newTestServer(t)
	srv.lastPulled = &pulledConfig{Inbounds: []inboundSpec{f.inbound("main", []uint16{port}, alice)}}

	panel := &statusPanel{}
	panel.status.Store(http.StatusServiceUnavailable)
	ts := httptest.NewServer(panel)
	defer ts.Close()
	pull := func() {
		srv.pullOnce(context.Background(), ts.Client(), config{PanelURL: ts.URL, ReportSecret: "secret"})
	}

	pull()
	if srv.node != nil {
		t.Fatal("started although nothing asked for it")
	}
	srv.restoreWanted = true
	pull()
	if srv.node == nil {
		t.Fatal("did not come back")
	}
	if srv.restoreWanted {
		t.Error("restoreWanted is still set")
	}
}

func TestPanelIsToldWhatCannotRun(t *testing.T) {
	srv := newTestServer(t)
	if srv.configError() != "" {
		t.Errorf("configError = %q on a fresh node", srv.configError())
	}
	srv.noteConfigRejectedLocked("digest", os.ErrPermission)
	if srv.configError() == "" {
		t.Error("configError is empty after a rejection")
	}
	if srv.rejected.retryAt.After(time.Now().Add(configRetryWhileDown)) {
		t.Errorf("nothing runs, yet the retry is %s away", time.Until(srv.rejected.retryAt))
	}
}

func TestThreeQuickRestartsGoBackToTheConfigBefore(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t)
	srv.stateDir = dir
	good := &pulledConfig{Version: "good", Inbounds: []inboundSpec{{Tag: "good", Protocol: "vmess", ListenPort: 1}}}
	bad := &pulledConfig{Version: "bad", Inbounds: []inboundSpec{{Tag: "bad", Protocol: "vmess", ListenPort: 2}}}
	if err := srv.persistConfig("previous-config.json", good); err != nil {
		t.Fatal(err)
	}
	if err := srv.persistConfig("last-good-config.json", bad); err != nil {
		t.Fatal(err)
	}

	for attempt, wantTag := range []string{"bad", "bad", "good"} {
		got := noteBootAttempt(dir)
		if got != attempt+1 {
			t.Fatalf("attempt %d counted as %d", attempt+1, got)
		}
		cfg, ok, crashed := loadBootConfig(dir, got)
		if !ok || cfg.Inbounds[0].Tag != wantTag {
			t.Errorf("attempt %d starts %+v, want %s", got, cfg.Inbounds, wantTag)
		}
		if (crashed != "") != (wantTag == "good") {
			t.Errorf("attempt %d: crashed digest %q", got, crashed)
		}
		if crashed != "" && crashed != configShapeDigest(*bad) {
			t.Errorf("the digest does not name the config that crashed")
		}
	}

	clearBootAttempts(dir)
	if got := noteBootAttempt(dir); got != 1 {
		t.Errorf("after the probation the count starts over, got %d", got)
	}
}

func TestBootWithNothingToGoBackToKeepsTheLastConfig(t *testing.T) {
	dir := t.TempDir()
	srv := newTestServer(t)
	srv.stateDir = dir
	only := &pulledConfig{Inbounds: []inboundSpec{{Tag: "only", Protocol: "vmess", ListenPort: 1}}}
	if err := srv.persistConfig("last-good-config.json", only); err != nil {
		t.Fatal(err)
	}
	cfg, ok, crashed := loadBootConfig(dir, maxBootAttempts+5)
	if !ok || cfg.Inbounds[0].Tag != "only" || crashed != "" {
		t.Errorf("cfg = %+v, ok = %v, crashed = %q", cfg, ok, crashed)
	}
	if _, ok, _ := loadBootConfig(t.TempDir(), 1); ok {
		t.Error("a config came out of an empty state directory")
	}
}
