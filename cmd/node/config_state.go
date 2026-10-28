package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
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
	names, secrets := map[string]bool{}, map[string]bool{}
	for i, u := range users {
		name := u.Name
		if name == "" {
			name = strconv.Itoa(i)
		}
		if names[name] {
			return fmt.Errorf("duplicate account name in %s", protocol)
		}
		names[name] = true
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
			return fmt.Errorf("missing credential for account %d in %s", i, protocol)
		}
		if secrets[secret] {
			return fmt.Errorf("duplicate credential in %s", protocol)
		}
		secrets[secret] = true
		if protocol == "tuic" && u.Password == "" {
			return fmt.Errorf("tuic password is required")
		}
		if protocol == "vless" && u.Flow != "" && u.Flow != "xtls-rprx-vision" {
			return fmt.Errorf("invalid vless flow")
		}
	}
	return nil
}
