package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Persist the same shared-set representation used by the panel: a 50k-user
// list shared by fifteen listeners is written once rather than fifteen times.
func (p pulledConfig) MarshalJSON() ([]byte, error) {
	type inboundPlain inboundSpec
	type inboundWire struct {
		*inboundPlain
		Users []userSpec `json:"users,omitempty"`
		Ref   string     `json:"users_ref"`
	}
	sets := map[string]json.RawMessage{}
	refs := map[*userSpec]string{}
	inbounds := make([]inboundWire, 0, len(p.Inbounds))
	for i := range p.Inbounds {
		in := &p.Inbounds[i]
		ref := "empty"
		if len(in.Users) > 0 {
			ref = refs[&in.Users[0]]
		}
		if ref == "" || ref == "empty" {
			raw, err := json.Marshal(in.Users)
			if err != nil {
				return nil, err
			}
			sum := sha256.Sum256(raw)
			ref = hex.EncodeToString(sum[:])
			sets[ref] = raw
			if len(in.Users) > 0 {
				refs[&in.Users[0]] = ref
			}
		}
		inbounds = append(inbounds, inboundWire{inboundPlain: (*inboundPlain)(in), Ref: ref})
	}
	return json.Marshal(struct {
		Version  string                     `json:"version"`
		Core     coreSpec                   `json:"core"`
		Inbounds []inboundWire              `json:"inbounds"`
		Sets     map[string]json.RawMessage `json:"user_sets"`
	}{p.Version, p.Core, inbounds, sets})
}

func (s *server) persistConfig(name string, value *pulledConfig) error {
	if s.stateDir == "" || value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicStateFile(filepath.Join(s.stateDir, name), raw)
}

func validateUserList(protocol string, users []userSpec) error {
	if _, problems := splitUsableUsers(protocol, users); len(problems) > 0 {
		return errors.New(problems[0])
	}
	return nil
}

// splitUsableUsers separates the accounts a core can serve from the ones it
// can't, and says why for each of those. A core refuses a whole list over one
// bad entry, so without this one user's bad row would stop every later change
// from reaching this node. No problems means users itself comes back: inbounds
// sharing a list keep sharing it.
func splitUsableUsers(protocol string, users []userSpec) ([]userSpec, []string) {
	names, secrets := make(map[string]bool, len(users)), make(map[string]bool, len(users))
	var problems []string
	var kept []userSpec
	for i, u := range users {
		problem := userProblem(protocol, i, u, names, secrets)
		if problem != "" && kept == nil {
			kept = append(make([]userSpec, 0, len(users)), users[:i]...)
		}
		if problem != "" {
			problems = append(problems, problem)
		} else if kept != nil {
			kept = append(kept, u)
		}
	}
	if kept == nil {
		return users, nil
	}
	return kept, problems
}

func userProblem(protocol string, i int, u userSpec, names, secrets map[string]bool) string {
	name := u.Name
	if name == "" {
		name = strconv.Itoa(i)
	}
	if names[name] {
		return fmt.Sprintf("duplicate account name in %s: %s", protocol, name)
	}
	secret := u.Password
	switch protocol {
	case "vmess", "vless", "tuic":
		secret = u.UUID
	case "snell":
		secret = u.UserKey
	case "naive":
		secret = u.Name
	}
	if secret == "" {
		return fmt.Sprintf("missing credential for account %d in %s: %s", i, protocol, name)
	}
	switch protocol {
	case "vmess", "vless", "tuic":
		if _, err := uuid.Parse(secret); err != nil {
			return fmt.Sprintf("invalid id in %s: %s", protocol, name)
		}
	}
	if secrets[secret] {
		return fmt.Sprintf("duplicate credential in %s: %s", protocol, name)
	}
	if protocol == "tuic" && u.Password == "" {
		return fmt.Sprintf("tuic password is required: %s", name)
	}
	if protocol == "vless" && u.Flow != "" && u.Flow != "xtls-rprx-vision" {
		return fmt.Sprintf("invalid vless flow: %s", name)
	}
	names[name] = true
	secrets[secret] = true
	return ""
}

// dropUnusableUsers applies splitUsableUsers to a whole config. Inbounds sharing
// one list are checked once; each list stays shared, and UserSets, which later
// deltas are applied to, stays what the panel sent.
func (s *server) dropUnusableUsers(cfg *pulledConfig) {
	type listKey struct {
		first    *userSpec
		count    int
		protocol string
	}
	checked := map[listKey][]userSpec{}
	for i := range cfg.Inbounds {
		in := &cfg.Inbounds[i]
		if len(in.Users) == 0 {
			continue
		}
		key := listKey{&in.Users[0], len(in.Users), in.Protocol}
		kept, seen := checked[key]
		if !seen {
			var problems []string
			kept, problems = splitUsableUsers(in.Protocol, in.Users)
			checked[key] = kept
			if len(problems) > 0 {
				s.logger.Error("accounts left out of the core", "inbound", in.Tag, "count", len(problems), "accounts", problems[:min(len(problems), 20)])
			}
		}
		in.Users = kept
	}
}

// panelRevocationGrace is how long the panel must keep refusing this node's
// secret before the node believes it was removed. One refusal proves little: a
// panel restored from an older backup, or a reverse proxy having a bad moment,
// can answer 401 for a while, and stopping the core disconnects every user here.
const panelRevocationGrace = 5 * time.Minute

// noteRefusedByPanel handles a 401 to this node's secret. Once the panel has
// refused it for the whole grace period the core stops, since a node nobody
// manages must not keep serving. The last config is moved aside, not deleted:
// it holds nothing a boot would use, and is what's left to diagnose from.
func (s *server) noteRefusedByPanel(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refusedSince.IsZero() {
		s.refusedSince = now
	}
	if now.Sub(s.refusedSince) < panelRevocationGrace {
		s.logger.Warn("panel refuses this node's secret; still serving", "since", s.refusedSince)
		return
	}
	if s.node == nil && s.lastPulled == nil {
		return
	}
	s.logger.Error("panel has refused this node's secret for too long; stopping the core", "since", s.refusedSince)
	_ = s.stopNodeLocked()
	s.lastPulled = nil
	s.rejected = nil
	s.restoreWanted = false
	if s.stateDir != "" {
		_ = os.Rename(filepath.Join(s.stateDir, "last-good-config.json"), filepath.Join(s.stateDir, "last-good-config.revoked.json"))
	}
}

// configRejection is the config this node last could not run. Without it a
// config that fails is tried again on every poll: stop the core, fail, restore
// the old one, and every user here is disconnected every ten seconds.
type configRejection struct {
	digest   string
	reason   string
	failures int
	retryAt  time.Time
}

// configRetryDelay is how long a rejected config rests. Trying it again can
// interrupt everyone on this node, so it doubles from five minutes to an hour.
func configRetryDelay(failures int) time.Duration {
	delay := 5 * time.Minute
	for i := 1; i < failures && delay < time.Hour; i++ {
		delay *= 2
	}
	return min(delay, time.Hour)
}

// configRetryWhileDown is the delay while no core runs: nobody can be
// interrupted, and the node is useless until something starts.
const configRetryWhileDown = 30 * time.Second

// configShapeDigest identifies a config by everything but its user lists, so a
// user change alone doesn't count as a new config to try.
func configShapeDigest(cfg pulledConfig) string {
	inbounds := make([]inboundSpec, len(cfg.Inbounds))
	for i, in := range cfg.Inbounds {
		in.Users = nil
		inbounds[i] = in
	}
	raw, err := json.Marshal(struct {
		Core     coreSpec      `json:"core"`
		Inbounds []inboundSpec `json:"inbounds"`
	}{cfg.Core, inbounds})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func (s *server) noteConfigRejectedLocked(digest string, err error) {
	failures := 1
	if s.rejected != nil && s.rejected.digest == digest {
		failures = s.rejected.failures + 1
	}
	delay := configRetryDelay(failures)
	if s.node == nil {
		delay = configRetryWhileDown
	}
	s.rejected = &configRejection{digest: digest, reason: err.Error(), failures: failures, retryAt: time.Now().Add(delay)}
	s.logger.Error("config rejected; next attempt delayed", "error", err, "failures", failures, "retry_in", delay)
}

// configRetryDueLocked reports whether a rejected config's rest is over.
func (s *server) configRetryDueLocked() bool {
	return s.rejected != nil && !time.Now().Before(s.rejected.retryAt)
}

// configError is what the panel is told this node can't run, "" when nothing.
func (s *server) configError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rejected == nil {
		return ""
	}
	return s.rejected.reason
}

// applyUsersOnlyLocked keeps user changes flowing while the config that carries
// them can't be applied: a revoked user must lose access even though some other
// part of the config is broken. What's recorded is the shape that actually runs
// with the users that actually run, under the version they arrived in, so the
// next delta has the base it expects.
func (s *server) applyUsersOnlyLocked(pulled pulledConfig) {
	if s.node == nil || s.lastPulled == nil {
		return
	}
	byTag := make(map[string]inboundSpec, len(pulled.Inbounds))
	for _, in := range pulled.Inbounds {
		byTag[in.Tag] = in
	}
	merged := *s.lastPulled
	merged.Inbounds = slices.Clone(s.lastPulled.Inbounds)
	for i := range merged.Inbounds {
		in := &merged.Inbounds[i]
		next, ok := byTag[in.Tag]
		if !ok || next.Protocol != in.Protocol || !hotUpdatableProtocol(in.Protocol) || reflect.DeepEqual(in.Users, next.Users) {
			continue
		}
		if in.Protocol == "shadowsocks" && shadowsocksMethod(in.Users) != shadowsocksMethod(next.Users) {
			continue
		}
		users := nodeUsers(next.Users)
		applied := true
		for _, listener := range s.plan.listenerTags(in.Tag) {
			if err := s.node.UpdateUsers(listener, in.Protocol, users); err != nil {
				s.logger.Error("hot-apply users of a rejected config", "tag", listener, "error", err)
				applied = false
				break
			}
		}
		if applied {
			in.Users = next.Users
		}
	}
	merged.Version, merged.UserSets = pulled.Version, pulled.UserSets
	s.lastPulled = &merged
	if err := s.persistConfig("last-good-config.json", s.lastPulled); err != nil {
		s.logger.Error("persist hot configuration", "error", err)
	}
}

// restoreIfDownLocked starts the last config again after a start that failed:
// a rollback that couldn't bind, or a boot before the panel was reachable. An
// operator's POST /stop clears restoreWanted, so this never undoes that.
func (s *server) restoreIfDownLocked() {
	if !s.restoreWanted || s.node != nil || s.lastPulled == nil {
		return
	}
	if err := s.startNodeLocked(startRequest{Inbounds: s.lastPulled.Inbounds, Core: &s.lastPulled.Core}); err != nil {
		s.logger.Error("core is down and the last config still won't start", "error", err)
		return
	}
	s.restoreWanted = false
	s.logger.Info("core restored from the last config")
}

// listenerNetworks is which sockets one listener of this protocol opens.
func listenerNetworks(protocol string) []string {
	switch protocol {
	case "hysteria", "hysteria2", "tuic":
		return []string{"udp"}
	case "shadowsocks":
		return []string{"tcp", "udp"}
	}
	return []string{"tcp"}
}

type listenSocket struct {
	network string
	port    uint16
}

// checkPortsDistinct refuses a config two of whose inbounds want one socket.
// The core would refuse it too, but only on start, after the running core has
// stopped; the panel not having caught it is no reason to disconnect anybody.
func checkPortsDistinct(inbounds []inboundSpec) error {
	owner := map[listenSocket]string{}
	for _, in := range inbounds {
		for _, port := range in.ports() {
			for _, network := range listenerNetworks(in.Protocol) {
				socket := listenSocket{network, port}
				if other, taken := owner[socket]; taken {
					return fmt.Errorf("%s port %d is claimed by both inbound %q and inbound %q", network, port, other, in.Tag)
				}
				owner[socket] = in.Tag
			}
		}
	}
	return nil
}

// checkPortsFree tries every socket a config would newly open, sparing the ones
// the running core already holds. A port taken by another process, this agent's
// own control port among them, would otherwise be found out by stopping the
// running core first.
func checkPortsFree(next, running []inboundSpec) error {
	held := map[listenSocket]bool{}
	for _, in := range running {
		for _, port := range in.ports() {
			for _, network := range listenerNetworks(in.Protocol) {
				held[listenSocket{network, port}] = true
			}
		}
	}
	for _, in := range next {
		for _, port := range in.ports() {
			for _, network := range listenerNetworks(in.Protocol) {
				if held[listenSocket{network, port}] {
					continue
				}
				// The address buildOptionsPlan has the core listen on.
				address := net.JoinHostPort("0.0.0.0", strconv.Itoa(int(port)))
				if network == "udp" {
					conn, err := net.ListenPacket("udp4", address)
					if err != nil {
						return fmt.Errorf("inbound %q: %w", in.Tag, err)
					}
					conn.Close()
					continue
				}
				listener, err := net.Listen("tcp4", address)
				if err != nil {
					return fmt.Errorf("inbound %q: %w", in.Tag, err)
				}
				listener.Close()
			}
		}
	}
	return nil
}

// A config that crashes this process some time after a clean start has already
// become last-good, so every restart would load it and crash again, for good. Three
// quick restarts in a row get the config from before it instead. Anything that
// crashes regardless of config just alternates between the two, which is no worse.
const (
	bootAttemptsFile  = "boot-attempts"
	maxBootAttempts   = 3
	bootProbation     = 2 * time.Minute
	crashLoopRejected = "this node kept crashing soon after starting with this config; it went back to the one before"
)

// noteBootAttempt counts this start and returns how many in a row have not got
// past the probation period.
func noteBootAttempt(stateDir string) int {
	path := filepath.Join(stateDir, bootAttemptsFile)
	attempts := 0
	if raw, err := os.ReadFile(path); err == nil {
		attempts, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	attempts++
	_ = atomicStateFile(path, []byte(strconv.Itoa(attempts)))
	return attempts
}

func clearBootAttempts(stateDir string) {
	_ = os.Remove(filepath.Join(stateDir, bootAttemptsFile))
}

// loadBootConfig reads the config to start with, and whether it's the fallback to
// the one before a config that kept crashing, along with that config's digest.
func loadBootConfig(stateDir string, attempts int) (cfg pulledConfig, ok bool, crashedDigest string) {
	var lastGood pulledConfig
	raw, err := os.ReadFile(filepath.Join(stateDir, "last-good-config.json"))
	if err != nil || json.Unmarshal(raw, &lastGood) != nil {
		return pulledConfig{}, false, ""
	}
	if attempts >= maxBootAttempts {
		var previous pulledConfig
		raw, err := os.ReadFile(filepath.Join(stateDir, "previous-config.json"))
		if err == nil && json.Unmarshal(raw, &previous) == nil && len(previous.Inbounds) > 0 {
			return previous, true, configShapeDigest(lastGood)
		}
	}
	return lastGood, true, ""
}
