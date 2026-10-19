// Package noderestart restarts the node agent (or the whole server) of a node
// over SSH. Nodes poll the panel; there is no channel from the panel to a node,
// so SSH - with the login a tunnel row stores for the node, or one supplied for
// a single call - is the only way in.
package noderestart

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Modes.
const (
	ModeService = "service"
	ModeReboot  = "reboot"
)

// ServiceName is the node agent's systemd unit.
const ServiceName = "rapido-go-node"

// Login is an SSH login.
type Login struct {
	Port     int32
	User     string
	Password string
}

// Request is what a restart call supplies.
type Request struct {
	Mode        string
	ConfirmName string
	// Login given in the request; used for this call only and never stored.
	Supplied *Login
}

// Credential sources.
const (
	SourceStored   = "tunnel"
	SourceSupplied = "request"
)

// Errors answered with 422.
var (
	ErrBadMode      = errors.New(`mode must be "service" or "reboot"`)
	ErrNoCredential = errors.New("no SSH login is stored for this node (no tunnel uses it) - send ssh_user and ssh_password in the request")
	ErrConfirmName  = errors.New("confirm_name must be exactly the node's name to reboot the server")
)

// Validate checks the mode, and for a reboot the typed confirmation, which must
// equal the node's name exactly (case-sensitive).
func Validate(nodeName string, req Request) error {
	switch req.Mode {
	case ModeService:
		return nil
	case ModeReboot:
		if req.ConfirmName == "" || req.ConfirmName != nodeName {
			return ErrConfirmName
		}
		return nil
	}
	return ErrBadMode
}

// ResolveLogin picks the login to use: the one stored on a tunnel row for the
// node, else the one supplied in the request. A stored login wins, so a stray
// password in the body is ignored when the panel already knows how to reach the
// node. It returns ErrNoCredential when neither exists or the supplied one is
// incomplete.
func ResolveLogin(stored *Login, supplied *Login) (Login, string, error) {
	if stored != nil && stored.User != "" {
		l := *stored
		if l.Port == 0 {
			l.Port = 22
		}
		return l, SourceStored, nil
	}
	if supplied != nil && supplied.User != "" && supplied.Password != "" {
		l := *supplied
		if l.Port <= 0 || l.Port > 65535 {
			l.Port = 22
		}
		return l, SourceSupplied, nil
	}
	return Login{}, "", ErrNoCredential
}

// Runner runs a command on the node.
type Runner interface {
	Run(ctx context.Context, command string) (string, error)
}

var userRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,31}$`)

// ValidUser reports whether s is an acceptable SSH user name.
func ValidUser(s string) bool { return userRE.MatchString(s) }

// privilege prefixes a command with sudo when the login is not root.
func privilege(user string) string {
	if user == "root" {
		return ""
	}
	return "sudo -n "
}

// Result is the answer to a restart call.
type Result struct {
	OK     bool   `json:"ok"`
	Mode   string `json:"mode"`
	Detail string `json:"detail"`
	// Active and ConfigPulled are only set for mode "service".
	Active       *bool `json:"active,omitempty"`
	ConfigPulled *bool `json:"config_pulled,omitempty"`
	TookMs       int64 `json:"took_ms"`
}

// Observation is one look at the restarted service.
type Observation struct {
	// State is what `systemctl is-active` printed (active, activating, failed,
	// inactive, ...).
	State string
	// Pulled is true once the journal shows the agent applied a config after
	// the restart.
	Pulled bool
}

// Verify settings: how long to wait for the agent to come back and pull its
// config, and how often to look.
const (
	VerifyBudget   = 25 * time.Second
	VerifyInterval = 2 * time.Second
)

// Verdict is Decide's conclusion.
type Verdict struct {
	Done   bool
	Active bool
	Pulled bool
	Detail string
}

// Decide looks at the observations so far (oldest first) and says whether
// waiting can stop and what the outcome is. It is done when the service is
// active and has pulled its config, when it has failed twice in a row, or when
// the time budget is used up.
func Decide(obs []Observation, elapsed, budget time.Duration) Verdict {
	if len(obs) == 0 {
		return Verdict{Detail: "no observation yet"}
	}
	last := obs[len(obs)-1]
	active := last.State == "active"
	switch {
	case active && last.Pulled:
		return Verdict{Done: true, Active: true, Pulled: true, Detail: "the node agent is active and applied its config again"}
	case len(obs) >= 2 && last.State == "failed" && obs[len(obs)-2].State == "failed":
		return Verdict{Done: true, Detail: "the node agent failed to start (systemctl reports: failed)"}
	case elapsed >= budget && active:
		return Verdict{Done: true, Active: true, Detail: fmt.Sprintf("the node agent is active but no \"pull config: applied\" line appeared within %s", budget)}
	case elapsed >= budget:
		return Verdict{Done: true, Detail: fmt.Sprintf("the node agent is not active after %s (systemctl reports: %s)", budget, last.State)}
	}
	return Verdict{Active: active, Pulled: last.Pulled}
}

var sinceRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)

// ParseStart reads the T0 line the restart script prints: the node's own clock
// just before the restart, used to look only at journal lines after it.
func ParseStart(out string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "T0:"); ok && sinceRE.MatchString(v) {
			return v, true
		}
	}
	return "", false
}

// parseState takes the first word of `systemctl is-active` output.
func parseState(out string) string {
	f := strings.Fields(out)
	if len(f) == 0 {
		return "unknown"
	}
	return f[0]
}

// RestartService restarts the node agent and waits until it is active and has
// pulled its config again (see Decide). WireGuard interfaces, HAProxy and frpc are
// separate services and are not touched. sleep and now are replaceable for
// tests.
func RestartService(ctx context.Context, r Runner, user string, sleep func(time.Duration), now func() time.Time) (Result, error) {
	started := now()
	res := Result{Mode: ModeService}
	sudo := privilege(user)

	out, err := r.Run(ctx, "echo \"T0:$(date '+%Y-%m-%d %H:%M:%S')\"; "+sudo+"systemctl restart "+ServiceName)
	since, haveSince := ParseStart(out)
	if err != nil {
		res.Detail = "systemctl restart failed: " + strings.TrimSpace(out)
		res.TookMs = now().Sub(started).Milliseconds()
		return res, nil
	}

	var obs []Observation
	for {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		stateOut, _ := r.Run(sctx, "systemctl is-active "+ServiceName)
		o := Observation{State: parseState(stateOut)}
		if haveSince && o.State == "active" {
			// The journal check: constant command; `since` is the node's own clock
			// reading, validated against a strict pattern above.
			cmd := fmt.Sprintf("%sjournalctl -u %s --since '%s' --no-pager -o cat | grep -cE 'pull config: (applied|hot-applied)'", sudo, ServiceName, since)
			cnt, _ := r.Run(sctx, cmd)
			o.Pulled = strings.TrimSpace(cnt) != "" && strings.TrimSpace(cnt) != "0"
		}
		cancel()
		obs = append(obs, o)
		elapsed := now().Sub(started)
		v := Decide(obs, elapsed, VerifyBudget)
		if v.Done || ctx.Err() != nil {
			res.OK = v.Done && v.Active && v.Pulled
			res.Detail = v.Detail
			active, pulled := v.Active, v.Pulled
			res.Active, res.ConfigPulled = &active, &pulled
			if ctx.Err() != nil && !v.Done {
				res.Detail = "stopped waiting: " + ctx.Err().Error()
			}
			res.TookMs = now().Sub(started).Milliseconds()
			return res, nil
		}
		sleep(VerifyInterval)
	}
}

// Reboot schedules a whole-server reboot, detached so the SSH session ends at
// once instead of hanging while the machine goes down.
func Reboot(ctx context.Context, r Runner, user string, now func() time.Time) (Result, error) {
	started := now()
	sudo := privilege(user)
	cmd := "setsid nohup sh -c 'sleep 2; " + sudo + "systemctl reboot' >/dev/null 2>&1 < /dev/null & echo scheduled"
	out, err := r.Run(ctx, cmd)
	res := Result{Mode: ModeReboot, TookMs: now().Sub(started).Milliseconds()}
	if err != nil || !strings.Contains(out, "scheduled") {
		res.Detail = "could not schedule the reboot: " + strings.TrimSpace(out)
		return res, nil
	}
	res.OK = true
	res.Detail = "The server will reboot in about two seconds. The node agent, WireGuard interfaces, HAProxy and frpc come back by themselves when it is up."
	return res, nil
}
