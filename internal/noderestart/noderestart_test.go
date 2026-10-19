package noderestart

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestValidateModeAndRebootConfirmation(t *testing.T) {
	if err := Validate("node2", Request{Mode: ModeService}); err != nil {
		t.Errorf("service needs no confirmation: %v", err)
	}
	if err := Validate("node2", Request{Mode: ModeReboot, ConfirmName: "node2"}); err != nil {
		t.Errorf("exact name: %v", err)
	}
	for _, bad := range []string{"", "Node2", "node2 ", "node", "NODE2"} {
		if err := Validate("node2", Request{Mode: ModeReboot, ConfirmName: bad}); !errors.Is(err, ErrConfirmName) {
			t.Errorf("confirm %q: %v", bad, err)
		}
	}
	for _, mode := range []string{"", "restart", "Service", "halt"} {
		if err := Validate("node2", Request{Mode: mode, ConfirmName: "node2"}); !errors.Is(err, ErrBadMode) {
			t.Errorf("mode %q: %v", mode, err)
		}
	}
	// an empty node name can never be "confirmed" by an empty string
	if err := Validate("", Request{Mode: ModeReboot}); !errors.Is(err, ErrConfirmName) {
		t.Errorf("empty name: %v", err)
	}
}

func TestResolveLogin(t *testing.T) {
	stored := &Login{Port: 2222, User: "root", Password: "stored-pw"}
	supplied := &Login{Port: 2200, User: "admin", Password: "given-pw"}

	l, src, err := ResolveLogin(stored, supplied)
	if err != nil || src != SourceStored || l.Password != "stored-pw" || l.Port != 2222 {
		t.Errorf("stored wins: %+v %s %v", l, src, err)
	}
	l, src, err = ResolveLogin(nil, supplied)
	if err != nil || src != SourceSupplied || l.User != "admin" || l.Port != 2200 {
		t.Errorf("supplied when nothing is stored: %+v %s %v", l, src, err)
	}
	if l, _, _ = ResolveLogin(&Login{User: "root", Password: "x"}, nil); l.Port != 22 {
		t.Errorf("default port: %+v", l)
	}
	if l, _, _ = ResolveLogin(nil, &Login{User: "u", Password: "p", Port: 99999}); l.Port != 22 {
		t.Errorf("out of range port falls back to 22: %+v", l)
	}
	for name, sup := range map[string]*Login{"nil": nil, "no user": {Password: "p"}, "no password": {User: "u"}} {
		if _, _, err := ResolveLogin(nil, sup); !errors.Is(err, ErrNoCredential) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, _, err := ResolveLogin(&Login{}, nil); !errors.Is(err, ErrNoCredential) {
		t.Errorf("an empty stored login is no login: %v", err)
	}
}

func TestValidUser(t *testing.T) {
	for _, ok := range []string{"root", "ubuntu", "deploy.user", "a-b_c"} {
		if !ValidUser(ok) {
			t.Errorf("%q", ok)
		}
	}
	for _, bad := range []string{"", "ro ot", "root;id", "$(id)", "1root", "a\nb"} {
		if ValidUser(bad) {
			t.Errorf("%q must be refused", bad)
		}
	}
}

func TestDecide(t *testing.T) {
	s := time.Second
	cases := []struct {
		name                 string
		obs                  []Observation
		elapsed              time.Duration
		done, active, pulled bool
	}{
		{"nothing yet", nil, 0, false, false, false},
		{"still activating", []Observation{{State: "activating"}}, 2 * s, false, false, false},
		{"active, no pull yet", []Observation{{State: "active"}}, 2 * s, false, true, false},
		{"active and pulled", []Observation{{State: "activating"}, {State: "active", Pulled: true}}, 4 * s, true, true, true},
		{"failed once may be a restart in progress", []Observation{{State: "failed"}}, 2 * s, false, false, false},
		{"failed twice is final", []Observation{{State: "failed"}, {State: "failed"}}, 4 * s, true, false, false},
		{"failed then active", []Observation{{State: "failed"}, {State: "active"}}, 4 * s, false, true, false},
		{"budget used, active without pull", []Observation{{State: "active"}}, 26 * s, true, true, false},
		{"budget used, inactive", []Observation{{State: "inactive"}}, 26 * s, true, false, false},
	}
	for _, c := range cases {
		v := Decide(c.obs, c.elapsed, VerifyBudget)
		if v.Done != c.done || v.Active != c.active || v.Pulled != c.pulled {
			t.Errorf("%s: %+v", c.name, v)
		}
		if v.Done && v.Detail == "" {
			t.Errorf("%s: a final verdict needs a detail", c.name)
		}
	}
}

func TestParseStart(t *testing.T) {
	if v, ok := ParseStart("noise\nT0:2026-10-19 12:00:01\n"); !ok || v != "2026-10-19 12:00:01" {
		t.Errorf("%q %v", v, ok)
	}
	for _, bad := range []string{"", "T0:", "T0:yesterday", "T0:2026-10-19 12:00:01'; reboot; '", "T0:2026-10-19"} {
		if _, ok := ParseStart(bad); ok {
			t.Errorf("%q must be refused", bad)
		}
	}
}

type fakeRunner struct {
	cmds    []string
	states  []string // successive `systemctl is-active` answers
	pulled  []string // successive journal counts
	failRun bool
}

func (f *fakeRunner) Run(_ context.Context, cmd string) (string, error) {
	f.cmds = append(f.cmds, cmd)
	switch {
	case strings.Contains(cmd, "systemctl restart"):
		if f.failRun {
			return "Failed to restart rapido-go-node.service: Unit not found.", errors.New("exit 5")
		}
		return "T0:2026-10-19 12:00:00\n", nil
	case strings.HasPrefix(cmd, "systemctl is-active"):
		s := f.states[0]
		if len(f.states) > 1 {
			f.states = f.states[1:]
		}
		return s + "\n", nil
	case strings.Contains(cmd, "journalctl"):
		p := f.pulled[0]
		if len(f.pulled) > 1 {
			f.pulled = f.pulled[1:]
		}
		return p + "\n", nil
	case strings.Contains(cmd, "systemctl reboot"):
		return "scheduled\n", nil
	}
	return "", errors.New("unexpected command " + cmd)
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time        { return c.t }
func (c *clock) sleep(d time.Duration) { c.t = c.t.Add(d) }

func TestRestartServiceVerifies(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	f := &fakeRunner{states: []string{"activating", "active", "active"}, pulled: []string{"0", "3"}}
	res, err := RestartService(context.Background(), f, "root", c.sleep, c.now)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || res.Active == nil || !*res.Active || res.ConfigPulled == nil || !*res.ConfigPulled || res.Mode != ModeService {
		t.Fatalf("res = %+v", res)
	}
	if res.TookMs < 2000 || res.TookMs > 10000 {
		t.Errorf("took %d ms", res.TookMs)
	}
	if !strings.Contains(f.cmds[0], "systemctl restart rapido-go-node") || strings.Contains(f.cmds[0], "sudo") {
		t.Errorf("restart command = %q", f.cmds[0])
	}
	var journal string
	for _, cmd := range f.cmds {
		if strings.Contains(cmd, "journalctl") {
			journal = cmd
		}
	}
	if !strings.Contains(journal, "--since '2026-10-19 12:00:00'") || !strings.Contains(journal, "pull config: (applied|hot-applied)") {
		t.Errorf("journal command = %q", journal)
	}
}

func TestRestartServiceUsesSudoForANonRootUser(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	f := &fakeRunner{states: []string{"active"}, pulled: []string{"1"}}
	res, _ := RestartService(context.Background(), f, "deploy", c.sleep, c.now)
	if !res.OK || !strings.Contains(f.cmds[0], "sudo -n systemctl restart rapido-go-node") {
		t.Errorf("res=%+v cmds=%v", res, f.cmds)
	}
}

func TestRestartServiceReportsFailureAndTimeouts(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	res, err := RestartService(context.Background(), &fakeRunner{failRun: true}, "root", c.sleep, c.now)
	if err != nil || res.OK || !strings.Contains(res.Detail, "Unit not found") {
		t.Errorf("restart refused: %+v %v", res, err)
	}

	c = &clock{t: time.Unix(1_800_000_000, 0)}
	res, _ = RestartService(context.Background(), &fakeRunner{states: []string{"failed"}, pulled: []string{"0"}}, "root", c.sleep, c.now)
	if res.OK || res.Active == nil || *res.Active || !strings.Contains(res.Detail, "failed to start") {
		t.Errorf("failed service: %+v", res)
	}

	c = &clock{t: time.Unix(1_800_000_000, 0)}
	res, _ = RestartService(context.Background(), &fakeRunner{states: []string{"active"}, pulled: []string{"0"}}, "root", c.sleep, c.now)
	if res.OK || res.Active == nil || !*res.Active || *res.ConfigPulled || !strings.Contains(res.Detail, "no \"pull config: applied\"") {
		t.Errorf("active but silent: %+v", res)
	}
	if res.TookMs < 25000 {
		t.Errorf("waited only %d ms", res.TookMs)
	}
}

func TestRebootIsDetachedAndNeedsNoWaiting(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	f := &fakeRunner{}
	res, _ := Reboot(context.Background(), f, "root", c.now)
	if !res.OK || res.Mode != ModeReboot || res.Active != nil {
		t.Fatalf("res = %+v", res)
	}
	cmd := f.cmds[0]
	for _, want := range []string{"setsid nohup sh -c 'sleep 2; systemctl reboot'", ">/dev/null 2>&1", "&"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command %q lacks %q", cmd, want)
		}
	}
	f = &fakeRunner{}
	Reboot(context.Background(), f, "deploy", c.now)
	if !strings.Contains(f.cmds[0], "sudo -n systemctl reboot") {
		t.Errorf("non-root: %q", f.cmds[0])
	}
}
