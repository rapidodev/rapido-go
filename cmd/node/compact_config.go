package main

import (
	"encoding/json"
	"fmt"
)

// Decode each shared credential set once. Inbound slices share immutable users
// until the next payload; old panels' inline-user schema remains supported.
func (p *pulledConfig) UnmarshalJSON(raw []byte) error {
	type plain pulledConfig
	var wire struct {
		Version  string                `json:"version"`
		Core     coreSpec              `json:"core"`
		Inbounds []json.RawMessage     `json:"inbounds"`
		Sets     map[string][]userSpec `json:"user_sets"`
		Base     string                `json:"base_version"`
		Deltas   map[string]struct {
			Base   string     `json:"base"`
			Upsert []userSpec `json:"upsert"`
			Remove []string   `json:"remove"`
		} `json:"user_sets_delta"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	if wire.Base != "" {
		if wire.Base != p.Version {
			return fmt.Errorf("delta base mismatch")
		}
		wire.Sets = map[string][]userSpec{}
		for ref, d := range wire.Deltas {
			base, ok := p.UserSets[d.Base]
			if !ok && d.Base != "" {
				return fmt.Errorf("delta credential base missing")
			}
			removed := map[string]bool{}
			for _, name := range d.Remove {
				removed[name] = true
			}
			upserts := map[string]userSpec{}
			for _, u := range d.Upsert {
				upserts[u.Name] = u
			}
			users := make([]userSpec, 0, len(base)+len(d.Upsert))
			for _, u := range base {
				if removed[u.Name] {
					continue
				}
				if changed, ok := upserts[u.Name]; ok {
					u = changed
					delete(upserts, u.Name)
				}
				users = append(users, u)
			}
			for _, u := range d.Upsert {
				if _, ok := upserts[u.Name]; ok {
					users = append(users, u)
					delete(upserts, u.Name)
				}
			}
			wire.Sets[ref] = users
		}
	}
	out := plain{Version: wire.Version, Core: wire.Core, UserSets: wire.Sets}
	for _, data := range wire.Inbounds {
		var in inboundSpec
		if err := json.Unmarshal(data, &in); err != nil {
			return err
		}
		var ref struct {
			Key string `json:"users_ref"`
		}
		if err := json.Unmarshal(data, &ref); err != nil {
			return err
		}
		if ref.Key != "" {
			users, ok := wire.Sets[ref.Key]
			if !ok {
				return fmt.Errorf("missing credential set %q", ref.Key)
			}
			in.Users = users
		}
		out.Inbounds = append(out.Inbounds, in)
	}
	*p = pulledConfig(out)
	return nil
}
